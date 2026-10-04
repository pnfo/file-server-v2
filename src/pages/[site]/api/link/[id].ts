import type { APIRoute } from 'astro'
import { json, parseId } from '../../../../lib/site.ts'

// Fresh signed link for a file page that has been open long enough for its links to expire (design §5.2).
export const GET: APIRoute = ({ locals, params, url }) => {
  const id = parseId(params.id)
  const e = locals.site && !Number.isNaN(id) ? locals.site.store.get(id) : undefined
  if (!locals.site || !e || e.kind !== 'file') return json({ error: 'not found' }, 404)
  const m = url.searchParams.get('mode'), mode = m === 'attachment' || m === 'stream' ? m : 'inline'
  return json(locals.site.store.link(e, mode))
}
