// S3 is the source of truth for which files exist, where they sit and their name/version/id/type.
// SQLite keeps everything else (downloads, dates, metadata, previews, published flag). See design §8.2.
import fs from 'node:fs'
import path from 'node:path'
import { APP_ROOT } from './config.ts'
import { today, type Entry } from './db.ts'
import { parseFileName } from './naming.ts'
import { listAll, type S3Object } from './s3.ts'
import type { SiteStore } from './store.ts'

export interface SyncReport {
  objects: number
  files: number
  folders: number
  added: number[]
  updated: number[]
  missing: number[]
  restored: number[]
  skipped: { key: string; reason: string }[]
  dryRun: boolean
}

type CountInfo = Record<string, { downloads?: number; dateAdded?: string }>
export function readCountsFile(file: string): CountInfo {
  const p = path.resolve(APP_ROOT, file)
  return fs.existsSync(p) ? JSON.parse(fs.readFileSync(p, 'utf-8')) : {}
}

// fields owned by S3; folder sizes are computed in the store, so they never count as a change
const signature = (e: Entry) =>
  JSON.stringify([e.parentId, e.kind, e.type, e.name, e.version, e.s3Key, e.kind === 'file' ? e.sizeBytes : 0])

const blank = (id: number, now: string): Entry => ({
  id, parentId: 0, kind: 'file', type: '', name: '', version: null, author: null, publishingYear: null,
  printedYear: null, description: null, s3Key: '', sizeBytes: 0, numEntries: 0, numPages: null,
  previewPages: null, downloads: 0, isPublished: true, dateAdded: today(), updatedAt: now, missingSince: null,
})

/** Pure part of the sync: turn a bucket listing into entries (no I/O), for testing. */
export function entriesFromListing(objects: S3Object[], rootPrefix: string) {
  const files = new Map<number, { key: string; parentId: number; name: string; version: string | null; type: string; size: number; date?: string }>()
  const folders = new Map<number, { key: string; parentId: number; name: string; version: string | null }>()
  const skipped: SyncReport['skipped'] = []
  for (const o of objects) {
    const rel = o.key.slice(rootPrefix.length + 1)
    const parts = rel.split('/')
    const fileName = parts.pop()!
    // root-level keys like thumbs/ and previews/ hold images, not entries
    if (/^(thumbs|previews|backups)$/.test(parts[0] || '') ) continue
    const f = parseFileName(fileName)
    if (!f.id) { skipped.push({ key: rel, reason: 'no {id} in file name' }); continue }
    if (!f.type) { skipped.push({ key: rel, reason: 'no file extension' }); continue }

    let parentId = 0
    let ok = true
    for (let i = 0; i < parts.length; i++) {
      const seg = parseFileName(parts[i])
      if (!seg.id) { parentId = -1; continue } // "-dev"-style segment: everything below is unlisted
      const key = parts.slice(0, i + 1).join('/')
      const existing = folders.get(seg.id)
      if (existing && existing.key !== key) { skipped.push({ key: rel, reason: `folder id ${seg.id} used by "${existing.key}" and "${key}"` }); ok = false; break }
      if (files.has(seg.id)) { skipped.push({ key: rel, reason: `folder id ${seg.id} is also a file id` }); ok = false; break }
      if (!existing) folders.set(seg.id, { key, parentId, name: seg.name, version: seg.version })
      parentId = seg.id
    }
    if (!ok) continue
    const dup = files.get(f.id) || folders.get(f.id)
    if (dup) { skipped.push({ key: rel, reason: `id ${f.id} already used by "${dup.key}"` }); continue }
    files.set(f.id, { key: rel, parentId, name: f.name, version: f.version, type: f.type, size: o.size, date: o.lastModified?.toISOString().slice(0, 10) })
  }
  return { files, folders, skipped }
}

export async function syncFromS3(store: SiteStore, opts: { dryRun?: boolean; counts?: CountInfo } = {}): Promise<SyncReport> {
  const root = store.cfg.s3RootFolder
  const objects = await listAll(`${root}/`)
  const { files, folders, skipped } = entriesFromListing(objects, root)
  const counts = opts.counts || {}
  const now = new Date().toISOString()
  const report: SyncReport = { objects: objects.length, files: files.size, folders: folders.size, added: [], updated: [], missing: [], restored: [], skipped, dryRun: !!opts.dryRun }
  const changed: Entry[] = []

  const apply = (id: number, patch: Partial<Entry>) => {
    const old = store.getAny(id)
    const e: Entry = old ? { ...old } : { ...blank(id, now), downloads: counts[id]?.downloads || 0, dateAdded: counts[id]?.dateAdded || patch.dateAdded || today() }
    delete (patch as any).dateAdded
    const before = signature(e)
    Object.assign(e, patch, { missingSince: null })
    if (!old) report.added.push(id)
    else if (old.missingSince) report.restored.push(id)
    else if (before !== signature(e)) report.updated.push(id)
    else return
    // a newly imported entry hasn't been changed yet: "updated" starts as the date it was added
    e.updatedAt = old ? now : e.dateAdded
    changed.push(e)
  }

  // a new folder takes the oldest date of the files below it
  const folderDate = new Map<number, string>()
  for (const [id, f] of files) {
    const d = counts[id]?.dateAdded || f.date || today()
    for (let p = folders.get(f.parentId), pid = f.parentId, guard = 0; p && guard < 50; pid = p.parentId, p = folders.get(pid), guard++) {
      if (!folderDate.has(pid) || d < folderDate.get(pid)!) folderDate.set(pid, d)
    }
  }
  for (const [id, f] of folders) apply(id, { kind: 'folder', type: 'folder', parentId: f.parentId, name: f.name, version: f.version, s3Key: f.key, sizeBytes: 0, dateAdded: folderDate.get(id) })
  for (const [id, f] of files) apply(id, { kind: 'file', type: f.type, parentId: f.parentId, name: f.name, version: f.version, s3Key: f.key, sizeBytes: f.size, dateAdded: f.date })

  // files that vanished from the bucket are kept (with their counts) but hidden until they reappear
  for (const e of store.all()) {
    if (e.kind === 'file' && !files.has(e.id) && !e.missingSince) {
      report.missing.push(e.id)
      changed.push({ ...e, missingSince: today(), updatedAt: now })
    }
  }
  if (!opts.dryRun) {
    store.db.saveMany(changed)
    store.db.setMeta('last_sync', now)
    store.reload()
  }
  return report
}
