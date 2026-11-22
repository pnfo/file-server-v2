// library.tipitaka.lk: serves the private R2 bucket "tipitaka-library".
//
// - <site>/previews/* and <site>/thumbs/* (PDF preview images) are public.
// - Everything else needs a link signed by the file server (server/sign.go):
//     Signature = base64url(HMAC-SHA256(LINK_SECRET, "GET\n<Expires>\n<path>[\n<response-content-disposition>]"))
//   Only those values are signed, so params a webview appends (fbclid, utm_*) do not break the link.
// - Database snapshots (*/backups/*, *-backups/*) are never served, signed or not.
//
// Both sides are tested against server/testdata/link_vectors.json (node worker/test.mjs).

interface R2Range { offset?: number; length?: number; suffix?: number }
interface R2Object {
  size: number
  httpEtag: string
  uploaded: Date
  httpMetadata?: { contentType?: string }
  range?: R2Range
  body?: ReadableStream
}
interface R2Bucket {
  get(key: string, options?: { range?: Headers; onlyIf?: Headers }): Promise<R2Object | null>
  head(key: string): Promise<R2Object | null>
}
export interface Env { BUCKET: R2Bucket; LINK_SECRET: string }

const SITE_URL = 'https://tipitaka.lk/library'
const enc = new TextEncoder()

// encodeStrict in server/jsutil.go: encodeURIComponent plus !'()*
export const encodeStrict = (s: string) =>
  encodeURIComponent(s).replace(/[!'()*]/g, c => '%' + c.charCodeAt(0).toString(16).toUpperCase())

/** The object key of a URL path, or null when the path is not valid percent-encoded UTF-8. */
export function keyOf(pathname: string): string | null {
  try { return pathname.split('/').slice(1).map(decodeURIComponent).join('/') } catch { return null }
}

/** The path as the file server signed it, whatever escapes the client chose to keep or change. */
export const canonicalPath = (key: string) => '/' + key.split('/').map(encodeStrict).join('/')

export const isPublic = (key: string) => /^[^/]+\/(previews|thumbs)\/[^/]+$/.test(key)
export const isForbidden = (key: string) => /(^|\/)backups\//.test(key) || /^[^/]*-backups\//.test(key)

const base64url = (b: ArrayBuffer) => btoa(String.fromCharCode(...new Uint8Array(b))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')

export async function sign(secret: string, expires: string, path: string, disposition: string | null): Promise<string> {
  const key = await crypto.subtle.importKey('raw', enc.encode(secret), { name: 'HMAC', hash: 'SHA-256' }, false, ['sign'])
  const sts = `GET\n${expires}\n${path}` + (disposition ? `\n${disposition}` : '')
  return base64url(await crypto.subtle.sign('HMAC', key, enc.encode(sts)))
}

function sameString(a: string, b: string): boolean {
  if (a.length !== b.length) return false
  let diff = 0
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i)
  return diff === 0
}

export type Verdict = { ok: true; key: string; disposition: string | null; public: boolean } | { ok: false; status: number; reason: string }

/** Decides whether a URL may be served. nowSeconds is passed in so tests can fix the clock. */
export async function verify(url: URL, secret: string, nowSeconds: number): Promise<Verdict> {
  const key = keyOf(url.pathname)
  if (!key || key.endsWith('/')) return { ok: false, status: 404, reason: 'not found' }
  if (isForbidden(key)) return { ok: false, status: 403, reason: 'forbidden' }
  if (isPublic(key)) return { ok: true, key, disposition: null, public: true }
  const expires = url.searchParams.get('Expires') ?? ''
  const signature = url.searchParams.get('Signature') ?? ''
  const disposition = url.searchParams.get('response-content-disposition')
  if (!/^\d{1,12}$/.test(expires) || !signature) return { ok: false, status: 403, reason: 'this link is not signed' }
  if (!secret) return { ok: false, status: 500, reason: 'LINK_SECRET is not set' }
  if (!sameString(signature, await sign(secret, expires, canonicalPath(key), disposition))) return { ok: false, status: 403, reason: 'bad signature' }
  if (Number(expires) < nowSeconds) return { ok: false, status: 403, reason: 'this link has expired' }
  return { ok: true, key, disposition, public: false }
}

const text = (status: number, body: string, headers: Record<string, string> = {}) =>
  new Response(body + '\n', { status, headers: { 'Content-Type': 'text/plain; charset=utf-8', 'Cache-Control': 'no-store', ...headers } })

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url)
    if (request.method !== 'GET' && request.method !== 'HEAD') return text(405, 'method not allowed', { Allow: 'GET, HEAD' })
    if (url.pathname === '/') return Response.redirect(SITE_URL, 302)
    const v = await verify(url, env.LINK_SECRET, Math.floor(Date.now() / 1000))
    // an expired or broken link usually comes from a page left open: send people where a fresh one is
    if (!v.ok) return text(v.status, `${v.reason} - get the file from ${SITE_URL}`)

    const headers = new Headers({ 'Accept-Ranges': 'bytes', 'X-Content-Type-Options': 'nosniff' })
    const describe = (o: R2Object) => {
      headers.set('ETag', o.httpEtag)
      headers.set('Last-Modified', o.uploaded.toUTCString())
      headers.set('Content-Type', o.httpMetadata?.contentType || 'application/octet-stream')
      if (v.disposition) headers.set('Content-Disposition', v.disposition)
      // preview URLs change with the file (?v=); signed links are identical for everyone within their window
      headers.set('Cache-Control', v.public ? 'public, max-age=31536000, immutable' : 'private, max-age=3600')
    }

    if (request.method === 'HEAD') {
      const o = await env.BUCKET.head(v.key)
      if (!o) return text(404, 'not found')
      describe(o)
      headers.set('Content-Length', String(o.size))
      return new Response(null, { headers })
    }

    let o: R2Object | null
    try {
      o = await env.BUCKET.get(v.key, { range: request.headers, onlyIf: request.headers })
    } catch {
      // R2 rejects a Range it cannot satisfy
      const h = await env.BUCKET.head(v.key)
      return h ? text(416, 'range not satisfiable', { 'Content-Range': `bytes */${h.size}` }) : text(404, 'not found')
    }
    if (!o) return text(404, 'not found')
    describe(o)
    if (!o.body) return new Response(null, { status: 304, headers }) // If-None-Match / If-Modified-Since matched

    const r = o.range
    if (r && request.headers.has('Range')) {
      const offset = r.suffix !== undefined ? Math.max(0, o.size - r.suffix) : r.offset ?? 0
      const length = r.suffix !== undefined ? o.size - offset : r.length ?? o.size - offset
      if (length < o.size) {
        headers.set('Content-Range', `bytes ${offset}-${offset + length - 1}/${o.size}`)
        headers.set('Content-Length', String(length))
        return new Response(o.body, { status: 206, headers })
      }
    }
    headers.set('Content-Length', String(o.size))
    return new Response(o.body, { headers })
  },
}
