import { createHmac } from 'node:crypto'
import { BUCKET, CDN_HOST } from './config.ts'

// SigV2 query-string auth for download links (file-server-v2-design.md §5.1).
// Unlike SigV4, the signature covers only the path, the expiry and response-* overrides, so params
// that webviews append (fbclid, utm_*) do not invalidate the link. Verified against Spaces 2026-10-01.

export const LINK_WINDOW_SECONDS = 6 * 3600

/** End of the next full window: links live 6-12 h, and everyone in a window gets an identical (CDN-cacheable) URL. */
export const windowEnd = (nowMs: number) => (Math.floor(nowMs / 1000 / LINK_WINDOW_SECONDS) + 2) * LINK_WINDOW_SECONDS

/** stream = no Content-Disposition override (for <audio>, matching what the old site's player received) */
export type LinkMode = 'inline' | 'attachment' | 'stream'

export interface SignInput {
  objectKey: string        // full key in the bucket, e.g. "library/අභිධර්ම{1}/x{2}.pdf"
  fileName: string         // clean name the browser should save as
  mode: LinkMode
  accessKeyId: string
  secretAccessKey: string
  nowMs?: number
  /** where the link points; default the Spaces CDN. The signature covers only the path, so any host that
   *  forwards the path + query to Spaces works (e.g. the droplet's /fs-dl/ nginx proxy, design §5.7). */
  baseUrl?: string
}

/** encodeURIComponent plus !'()* - some media loaders (Safari/AVFoundation) mishandle those raw in URLs. */
export const encodeStrict = (s: string) => encodeURIComponent(s).replace(/[!'()*]/g, c => '%' + c.charCodeAt(0).toString(16).toUpperCase())
const encodePath = (key: string) => key.split('/').map(encodeStrict).join('/')

export function contentDisposition(mode: LinkMode, fileName: string, asciiFallback: string) {
  return `${mode}; filename="${asciiFallback}"; filename*=UTF-8''${encodeStrict(fileName)}`
}

export function signLink({ objectKey, fileName, mode, accessKeyId, secretAccessKey, nowMs = Date.now(), baseUrl = `https://${CDN_HOST}` }: SignInput) {
  const expires = windowEnd(nowMs)
  const ext = fileName.includes('.') ? fileName.slice(fileName.lastIndexOf('.')) : ''
  const cd = mode === 'stream' ? null : contentDisposition(mode, fileName, `file${ext.replace(/[^.\w]/g, '')}`)
  const path = `/${encodePath(objectKey)}`
  // response-* values go into the string-to-sign un-encoded
  const stringToSign = `GET\n\n\n${expires}\n/${BUCKET}${path}${cd ? `?response-content-disposition=${cd}` : ''}`
  const signature = createHmac('sha1', secretAccessKey).update(stringToSign, 'utf8').digest('base64')
  const url = `${baseUrl}${path}?AWSAccessKeyId=${encodeStrict(accessKeyId)}&Expires=${expires}`
    + `&Signature=${encodeStrict(signature)}${cd ? `&response-content-disposition=${encodeStrict(cd)}` : ''}`
  return { url, expires }
}
