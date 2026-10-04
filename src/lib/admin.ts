// Admin operations. Upload bytes never pass through Node: the browser PUTs straight to Spaces with a
// presigned URL, then calls commit (design §7). Every write rebuilds the in-memory index.
import { today, type Entry } from './db.ts'
import { getTypeInfo } from './format.ts'
import { buildFileName, buildFolderName, parseFileName } from './naming.ts'
import * as s3 from './s3.ts'
import { sanitizeHtml } from './sanitize.ts'
import type { SiteStore } from './store.ts'

export class AdminError extends Error {}
const fail = (msg: string): never => { throw new AdminError(msg) }

const objectKey = (store: SiteStore, rel: string) => `${store.cfg.s3RootFolder}/${rel}`
const parentPath = (store: SiteStore, parentId: number) => {
  if (!parentId) return ''
  const p = store.getAny(parentId)
  if (!p || p.kind !== 'folder') fail(`folder ${parentId} not found`)
  return p!.s3Key + '/'
}
const cleanText = (s: unknown, max = 300) => (typeof s === 'string' && s.trim() ? s.trim().slice(0, max) : null)
const year = (v: unknown) => { const n = Number(v); return Number.isInteger(n) && n > 0 && n < 3000 ? n : null }

// ids handed out for uploads in progress, so two parallel uploads never get the same {id}
const reserved = new Set<number>()
function reserveId(store: SiteStore) {
  let id = store.nextId()
  while (reserved.has(id)) id++
  reserved.add(id)
  return id
}

export interface MetaInput {
  name?: string; version?: string | null; author?: string | null; publishingYear?: unknown; printedYear?: unknown
  description?: string | null; isPublished?: boolean
}

export async function saveMeta(store: SiteStore, id: number, input: MetaInput) {
  const e = store.getAny(id) ?? fail(`entry ${id} not found`)
  const next: Entry = { ...e }
  if (input.name !== undefined) next.name = cleanText(input.name) ?? fail('name is required')
  if (input.version !== undefined) next.version = cleanText(input.version, 100)
  if (input.author !== undefined) next.author = cleanText(input.author)
  if (input.publishingYear !== undefined) next.publishingYear = year(input.publishingYear)
  if (input.printedYear !== undefined) next.printedYear = year(input.printedYear)
  if (input.description !== undefined) next.description = sanitizeHtml(input.description)
  if (input.isPublished !== undefined) next.isPublished = !!input.isPublished

  // name and version live in the S3 key, so changing them renames the object
  if (next.name !== e.name || next.version !== e.version) {
    const dir = e.s3Key.includes('/') ? e.s3Key.slice(0, e.s3Key.lastIndexOf('/') + 1) : ''
    if (e.kind === 'folder') {
      if (store.all().some(c => c.parentId === e.id)) fail('folders with contents cannot be renamed here (every file key would change) - rename in the bucket and run sync')
      next.s3Key = dir + buildFolderName(next.name, e.id)
    } else {
      next.s3Key = dir + buildFileName(next.name, next.version, e.id, e.type)
      await s3.move(objectKey(store, e.s3Key), objectKey(store, next.s3Key))
    }
  }
  store.db.save(store.touch(next))
  store.reload()
  return next
}

export function createFolder(store: SiteStore, parentId: number, name: string) {
  const n = cleanText(name) ?? fail('name is required')
  const id = reserveId(store)
  const e: Entry = {
    id, parentId, kind: 'folder', type: 'folder', name: n, version: null, author: null, publishingYear: null, printedYear: null,
    description: null, s3Key: parentPath(store, parentId) + buildFolderName(n, id), sizeBytes: 0, numEntries: 0, numPages: null,
    previewPages: null, downloads: 0, isPublished: true, dateAdded: today(), updatedAt: new Date().toISOString(), missingSince: null,
  }
  store.db.save(e)
  reserved.delete(id)
  store.reload()
  return e
}

export interface UploadRequest { parentId: number; fileName: string; name?: string; version?: string | null; contentType?: string }

export async function startUpload(store: SiteStore, req: UploadRequest) {
  const parsed = parseFileName(req.fileName)
  const type = (parsed.type || fail('file has no extension')).toLowerCase()
  const name = cleanText(req.name) ?? parsed.name
  const version = req.version !== undefined ? cleanText(req.version, 100) : parsed.version
  const id = reserveId(store)
  const rel = parentPath(store, req.parentId) + buildFileName(name, version, id, type)
  const contentType = req.contentType || getTypeInfo(type).mime || 'application/octet-stream'
  return { id, key: rel, name, version, type, contentType, url: await s3.presignPut(objectKey(store, rel), contentType) }
}

export async function commitUpload(store: SiteStore, b: { id: number; key: string; parentId: number; name: string; version: string | null; type: string; size: number } & MetaInput) {
  if (!reserved.has(b.id)) fail('unknown upload - start again')
  if (!(await s3.exists(objectKey(store, b.key)))) fail('the file did not reach the bucket')
  const e: Entry = {
    id: b.id, parentId: b.parentId, kind: 'file', type: b.type, name: b.name, version: b.version,
    author: cleanText(b.author), publishingYear: year(b.publishingYear), printedYear: year(b.printedYear),
    description: sanitizeHtml(b.description), s3Key: b.key, sizeBytes: Number(b.size) || 0, numEntries: 0, numPages: null,
    previewPages: null, downloads: 0, isPublished: b.isPublished ?? true, dateAdded: today(), updatedAt: new Date().toISOString(), missingSince: null,
  }
  store.db.save(e)
  reserved.delete(b.id)
  store.reload()
  return e
}

export async function previewUploadUrls(store: SiteStore, id: number, count: number) {
  const e = store.getAny(id) ?? fail(`entry ${id} not found`)
  if (e.type !== 'pdf') fail('previews are for PDFs only')
  if (count < 1 || count > MAX_PREVIEWS) fail(`choose 1-${MAX_PREVIEWS} pages`)
  return Promise.all(Array.from({ length: count }, (_, i) => s3.presignPut(previewKey(store, id, i), 'image/jpeg', { publicRead: true })))
}

const MAX_PREVIEWS = 6
const previewKey = (store: SiteStore, id: number, i: number) => objectKey(store, `previews/${id}-${i}.jpg`)

export async function savePreviews(store: SiteStore, id: number, pages: number[], numPages: number | null) {
  const e = store.getAny(id) ?? fail(`entry ${id} not found`)
  const next = { ...e, previewPages: pages.length ? pages.map(Number) : null, numPages: numPages || e.numPages }
  store.db.save(store.touch(next))
  store.reload()
  // fewer pages than before: remove the images that are no longer used
  for (let i = pages.length; i < MAX_PREVIEWS; i++) await s3.remove(previewKey(store, id, i)).catch(() => {})
  return next
}

export async function deleteEntry(store: SiteStore, id: number) {
  const e = store.getAny(id) ?? fail(`entry ${id} not found`)
  if (e.kind === 'folder') {
    if (store.all().some(c => c.parentId === e.id)) fail('folder is not empty')
  } else {
    await s3.remove(objectKey(store, e.s3Key))
    for (let i = 0; i < MAX_PREVIEWS; i++) await s3.remove(previewKey(store, id, i)).catch(() => {})
  }
  store.db.delete(id)
  store.reload()
}

export const sourceUrl = (store: SiteStore, id: number) => {
  const e = store.getAny(id) ?? fail(`entry ${id} not found`)
  return s3.presignGet(objectKey(store, e.s3Key))
}
