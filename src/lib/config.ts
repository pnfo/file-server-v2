import fs from 'node:fs'
import path from 'node:path'

export type SiteId = 'library' | 'cloud'
export const SITE_IDS: SiteId[] = ['library', 'cloud']

export interface SiteConfig {
  id: SiteId
  fileTypeName: string
  rootFolderName: string
  webUrlRoot: string
  s3RootFolder: string
  ogImage: string
  displayThumbs: boolean
  idToInfoFile?: string
}

export interface Secrets {
  adminPassword: string
  sessionSecret: string
  s3: { accessKeyId: string; secretAccessKey: string }
  signer?: { accessKeyId: string; secretAccessKey: string }
}

export const APP_ROOT = process.env.APP_ROOT || process.cwd()
export const BUCKET = 'tipitaka'
export const S3_ENDPOINT = 'https://sgp1.digitaloceanspaces.com'
export const CDN_HOST = 'tipitaka.sgp1.cdn.digitaloceanspaces.com'

const readJson = (file: string) => JSON.parse(fs.readFileSync(path.join(APP_ROOT, file), 'utf-8'))

const siteConfigs = new Map<SiteId, SiteConfig>()
export function getSiteConfig(id: SiteId): SiteConfig {
  let cfg = siteConfigs.get(id)
  if (!cfg) {
    cfg = { id, displayThumbs: false, ...readJson(`config/${id}.json`) } as SiteConfig
    siteConfigs.set(id, cfg)
  }
  return cfg
}

let secrets: Secrets | undefined
export function getSecrets(): Secrets {
  if (!secrets) {
    secrets = readJson('config/secrets.json') as Secrets
    if (!secrets.signer) {
      console.warn('config/secrets.json has no "signer" key - signing download links with the read/write key. Create a read-only Spaces key.')
      secrets.signer = secrets.s3
    }
  }
  return secrets
}

/**
 * URL prefixes that map to a site. `SITE_ALIASES=library-v2:library,cloud-v2:cloud` lets the new server
 * run side by side with production during testing; alias pages are marked noindex.
 */
const prefixes = new Map<string, { site: SiteId; isAlias: boolean }>(SITE_IDS.map(id => [id, { site: id, isAlias: false }]))
for (const pair of (process.env.SITE_ALIASES || '').split(',').filter(Boolean)) {
  const [prefix, site] = pair.split(':').map(s => s.trim())
  if (SITE_IDS.includes(site as SiteId)) prefixes.set(prefix, { site: site as SiteId, isAlias: true })
}
export const resolvePrefix = (prefix: string) => prefixes.get(prefix)
/** URL prefix of `site` on the same deployment flavour (production or test alias), e.g. for a site switcher. */
export function prefixFor(site: SiteId, isAlias: boolean) {
  for (const [prefix, r] of prefixes) if (r.site === site && r.isAlias === isAlias) return prefix
  return site
}
