import type { APIRoute } from 'astro'
import { clientKey, parseId } from '../../../../lib/site.ts'

// navigator.sendBeacon target from the file page (design §5.3). Always 204, counted at most once per client per 10 min.
export const POST: APIRoute = ({ locals, params, request, clientAddress }) => {
  const id = parseId(params.id)
  if (locals.site && !Number.isNaN(id)) locals.site.store.hit(id, clientKey(request, clientAddress))
  return new Response(null, { status: 204, headers: { 'Cache-Control': 'no-store' } })
}
