// Bucket naming convention, unchanged from production: name[version]{id}.type
// Folders are path segments "name{id}"; a segment without {id} makes everything below it unlisted.

export interface ParsedName { name: string; version: string | null; id: number; type: string | null }

export function parseFileName(fileName: string): ParsedName {
  const m = /^(.+?)(?:\[(.*)\])?(?:\{(\d+)\})?(?:\.(\w+))?$/.exec(fileName)
  if (!m) return { name: fileName, version: null, id: 0, type: null }
  return { name: m[1].trim(), version: m[2]?.trim() || null, id: Number(m[3] || 0), type: m[4]?.toLowerCase() || null }
}

const clean = (s: string) => s.replace(/[\\/{}\[\]]/g, ' ').replace(/\s+/g, ' ').trim()

export function buildFileName(name: string, version: string | null, id: number, type: string) {
  return `${clean(name)}${version ? `[${clean(version)}]` : ''}{${id}}.${type}`
}
export const buildFolderName = (name: string, id: number) => `${clean(name)}{${id}}`

/** Name the browser saves the file as - no [version]{id} noise, version kept when present. */
export function saveAsName(e: { name: string; version: string | null; type: string }) {
  const base = clean(e.name) + (e.version ? ` - ${clean(e.version)}` : '')
  return `${base}.${e.type}`
}
