import type { APIRoute } from 'astro'
import { json } from '../../../lib/site.ts'
import { checkSigner } from '../../../lib/selfcheck.ts'

export const GET: APIRoute = async ({ locals }) => {
  const s = locals.site
  if (!s) return json({ error: 'not found' }, 404)
  const signer = await checkSigner(s.store)
  return json({
    ok: signer.ok, site: s.id, entries: s.store.size, lastSync: s.store.db.getMeta('last_sync') || null,
    signer, rssMb: Math.round(process.memoryUsage().rss / 1048576), uptimeS: Math.round(process.uptime()),
  }, signer.ok ? 200 : 503)
}
