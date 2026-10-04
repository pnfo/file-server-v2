import type { APIRoute } from 'astro'
// the header search form is a plain GET form (works without JS): /search?q=x -> /search/x
export const GET: APIRoute = ({ locals, url, redirect }) => {
  if (!locals.site) return new Response('Not found', { status: 404 })
  const q = (url.searchParams.get('q') || '').trim().toLowerCase()
  return redirect(q ? `${locals.site.base}/search/${encodeURIComponent(q)}` : `${locals.site.base}/`, 302)
}
