import type { APIRoute } from 'astro'
import { parseId } from '../../../lib/site.ts'

// Old-site links like /library/601/all (still requested by crawlers): send them to the entry page.
export const GET: APIRoute = ({ locals, params, redirect }) => {
  const id = parseId(params.id)
  if (!locals.site || Number.isNaN(id)) return new Response('Not found', { status: 404 })
  return redirect(`${locals.site.base}/${id}`, 301)
}
