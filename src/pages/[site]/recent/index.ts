import type { APIRoute } from 'astro'
export const GET: APIRoute = ({ locals, url, redirect }) => {
  if (!locals.site) return new Response('Not found', { status: 404 })
  const m = parseInt(url.searchParams.get('m') || '3', 10) || 3
  return redirect(`${locals.site.base}/recent/${m}`, 302)
}
