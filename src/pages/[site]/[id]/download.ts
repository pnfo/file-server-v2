import type { APIRoute } from 'astro'
import { clientKey, parseId } from '../../../lib/site.ts'

// Legacy /{id}/download links (shared on social media, embedded on other sites). Design §5.4:
// count the hit, then 302 straight to the signed CDN URL. No HTML bridge page - a 302 leaves no
// history entry, so Back works. LEGACY_DOWNLOAD_MODE=redirect-page sends browser navigations to the
// file page instead (fallback if mobile browsers stall on the cross-origin redirect).
export const GET: APIRoute = ({ locals, params, request, clientAddress, redirect }) => {
  const s = locals.site
  const id = parseId(params.id)
  const e = s && !Number.isNaN(id) ? s.store.get(id) : undefined
  if (!s || !e) return new Response('Not found', { status: 404 })
  if (e.kind === 'folder') return redirect(`${s.base}/${e.id}`, 302)

  const isNavigation = request.headers.get('sec-fetch-dest') === 'document'
  if (process.env.LEGACY_DOWNLOAD_MODE === 'redirect-page' && isNavigation) return redirect(`${s.base}/${e.id}`, 302)

  s.store.hit(e.id, clientKey(request, clientAddress))
  return new Response(null, { status: 302, headers: { Location: s.store.link(e, 'inline').url, 'Cache-Control': 'no-store' } })
}
