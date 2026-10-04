import { getPossibleMatches } from '@pnfo/singlish-search'
import { getSecrets, getSiteConfig, SITE_IDS, type SiteConfig, type SiteId } from './config.ts'
import { SiteDb, type Entry } from './db.ts'
import { saveAsName } from './naming.ts'
import { signLink, type LinkMode } from './sign.ts'

const collator = new Intl.Collator('si')
const byName = (a: Entry, b: Entry) =>
  (a.kind === b.kind ? 0 : a.kind === 'folder' ? -1 : 1) || collator.compare(a.name, b.name) || a.id - b.id

export const SEARCH_MAX_RESULTS = 50
const DROPLET_SHARE = Math.min(100, Math.max(0, Number(process.env.DROPLET_SHARE) || 0))
const DROPLET_BASE = (process.env.DROPLET_BASE || '').replace(/\/$/, '') // e.g. https://tipitaka.lk/fs-dl
const SEARCH_MAX_TERMS = 300
const escapeRegExp = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')

export type SearchResult = { entries: Entry[]; error?: 'too-many-terms' }

/**
 * Everything a public page reads comes from memory; SQLite is only persistence.
 * Rebuilt from the DB after every admin write or sync (a few thousand rows, a few ms).
 */
export class SiteStore {
  readonly site: SiteId
  readonly cfg: SiteConfig
  readonly db: SiteDb
  private byId = new Map<number, Entry>()
  private children = new Map<number, Entry[]>()
  private blocked = new Set<number>()
  loadedAt = 0

  constructor(site: SiteId, db?: SiteDb) {
    this.site = site
    this.cfg = getSiteConfig(site)
    this.db = db || new SiteDb(site)
    this.reload()
  }

  reload() {
    const all = this.db.all()
    const byId = new Map(all.map(e => [e.id, e]))
    // an unpublished folder hides its whole subtree
    const blocked = new Set<number>()
    for (const e of all) {
      for (let p = byId.get(e.parentId), guard = 0; p && guard < 50; p = byId.get(p.parentId), guard++) {
        if (!p.isPublished) { blocked.add(e.id); break }
      }
    }
    this.blocked = blocked
    // folder totals: published files that are not missing, rolled up through every ancestor
    for (const f of all) {
      if (f.kind !== 'file' || !f.isPublished || f.missingSince || blocked.has(f.id)) continue
      for (let p = byId.get(f.parentId), guard = 0; p && guard < 50; p = byId.get(p.parentId), guard++) {
        p.numEntries++; p.sizeBytes += f.sizeBytes
      }
    }
    const children = new Map<number, Entry[]>()
    for (const e of all) {
      if (!this.isVisible(e)) continue
      const list = children.get(e.parentId) || []
      list.push(e); children.set(e.parentId, list)
    }
    for (const list of children.values()) list.sort(byName)
    this.byId = byId; this.children = children; this.loadedAt = Date.now()
  }

  private isVisible(e: Entry) {
    return e.isPublished && !e.missingSince && !this.blocked.has(e.id) && (e.kind === 'file' || e.numEntries > 0)
  }

  get size() { return this.byId.size }
  /** Published entry, or undefined (unpublished/missing entries are 404 on public pages). */
  get(id: number) { const e = this.byId.get(id); return e && this.isVisible(e) ? e : undefined }
  getAny(id: number) { return this.byId.get(id) }
  all() { return [...this.byId.values()] }
  childrenOf(id: number) { return this.children.get(id) || [] }

  /** Ancestors from the root down, excluding the entry itself. */
  parents(id: number): Entry[] {
    const chain: Entry[] = []
    for (let p = this.byId.get(this.byId.get(id)?.parentId ?? 0), guard = 0; p && guard < 50; p = this.byId.get(p.parentId), guard++) {
      chain.unshift(p)
    }
    return chain
  }
  parentOf(e: Entry) { return this.byId.get(e.parentId) }

  search(query: string, max = SEARCH_MAX_RESULTS): SearchResult {
    const q = query.trim().toLowerCase()
    if (!q) return { entries: [] }
    let terms: string[]
    try { terms = getPossibleMatches(q) } catch { terms = [] }
    if (terms.length > SEARCH_MAX_TERMS) return { entries: [], error: 'too-many-terms' }
    const re = new RegExp([...terms, q].map(escapeRegExp).join('|'), 'i')
    const out: Entry[] = []
    for (const e of this.all().sort(byName)) {
      if (!this.isVisible(e)) continue
      if (re.test(e.name) || (e.version && re.test(e.version)) || (e.author && re.test(e.author))) {
        out.push(e)
        if (out.length >= max) break
      }
    }
    return { entries: out }
  }

  recent(days: number): Entry[] {
    const since = new Date(Date.now() - days * 86400_000).toISOString().slice(0, 10)
    return this.all().filter(e => e.kind === 'file' && this.isVisible(e) && e.dateAdded > since)
      .sort((a, b) => b.dateAdded.localeCompare(a.dateAdded) || collator.compare(a.name, b.name))
  }

  // ---- downloads ---------------------------------------------------------------------------

  private recentHits = new Map<string, number>()
  /** Count a download, ignoring repeats of the same (client, file) within 10 minutes. */
  hit(id: number, clientKey: string): boolean {
    const e = this.get(id)
    if (!e || e.kind !== 'file') return false
    const key = `${clientKey}|${id}`, now = Date.now(), last = this.recentHits.get(key)
    if (last && now - last < 10 * 60_000) return false
    if (this.recentHits.size > 20_000) this.recentHits.clear()
    this.recentHits.set(key, now)
    this.db.hit(id)
    e.downloads++
    return true
  }

  link(e: Entry, mode: LinkMode, nowMs = Date.now()) {
    const { accessKeyId, secretAccessKey } = getSecrets().signer!
    // DROPLET_SHARE % of links go through the droplet's nginx proxy instead of the CDN, to use the droplet
    // transfer pool (Spaces -> Droplet in SGP1 is free) and keep Spaces egress under its allowance.
    const baseUrl = DROPLET_BASE && Math.random() * 100 < DROPLET_SHARE ? DROPLET_BASE : undefined
    return signLink({ objectKey: `${this.cfg.s3RootFolder}/${e.s3Key}`, fileName: saveAsName(e), mode, accessKeyId, secretAccessKey, nowMs, baseUrl })
  }

  /** Public image URLs for the PDF preview gallery. */
  previews(e: Entry): { page: number | null; url: string }[] {
    if (e.type !== 'pdf') return []
    const base = `https://tipitaka.sgp1.cdn.digitaloceanspaces.com/${this.cfg.s3RootFolder}`
    if (e.previewPages?.length) return e.previewPages.map((page, i) => ({ page, url: `${base}/previews/${e.id}-${i}.jpg?v=${e.updatedAt}` }))
    if (!this.cfg.displayThumbs) return []
    // old thumbnails (thumb-generator.js): page 1 and the middle page, floor(n/2) counted from 0; one image for 1-page PDFs
    const n = e.numPages
    const pages = n ? (n > 1 ? [1, Math.floor(n / 2) + 1] : [1]) : [null, null]
    return pages.map((page, i) => ({ page, url: `${base}/thumbs/${e.id}-${i}.jpg` }))
  }

  touch(e: Entry) { e.updatedAt = new Date().toISOString(); return e }
  nextId() { return Math.max(0, ...this.byId.keys()) + 1 }
}

let stores: Map<SiteId, SiteStore> | undefined
export function getStore(site: SiteId): SiteStore {
  if (!stores) stores = new Map()
  let s = stores.get(site)
  if (!s) {
    s = new SiteStore(site)
    stores.set(site, s)
    console.log(`[${site}] loaded ${s.size} entries`)
    if (!s.size) console.warn(`[${site}] database is empty - run "npm run sync -- ${site}"`)
  }
  return s
}
export const allStores = () => SITE_IDS.map(getStore)
