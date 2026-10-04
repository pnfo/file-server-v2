// Verifies Spaces still accepts our SigV2 links (and still ignores appended params like fbclid).
// If DigitalOcean ever drops SigV2 this fails loudly; switch fileLink to a fallback from design §2.1.
import type { SiteStore } from './store.ts'

let cached: { at: number; result: { ok: boolean; status?: number; withFbclid?: number; error?: string } } | undefined

export async function checkSigner(store: SiteStore) {
  if (cached && Date.now() - cached.at < 10 * 60_000) return cached.result
  const sample = store.all().find(e => e.kind === 'file' && !e.missingSince)
  let result: NonNullable<typeof cached>['result']
  if (!sample) result = { ok: true, error: 'no files to test' }
  else {
    try {
      const { url } = store.link(sample, 'inline')
      const get = (u: string) => fetch(u, { headers: { Range: 'bytes=0-0' } }).then(r => { r.body?.cancel(); return r.status })
      const [status, withFbclid] = await Promise.all([get(url), get(url + '&fbclid=selfcheck')])
      result = { ok: status === 206 && withFbclid === 206, status, withFbclid }
    } catch (err) {
      result = { ok: false, error: String(err) }
    }
  }
  if (!result.ok) console.error('SIGNER SELF-CHECK FAILED', result)
  cached = { at: Date.now(), result }
  return result
}
