import type { APIRoute } from 'astro'
import { COOKIE } from '../../../lib/auth.ts'
export const POST: APIRoute = ({ locals, cookies, redirect }) => {
  cookies.delete(COOKIE, { path: `${locals.site!.base}/admin` })
  return redirect(`${locals.site!.base}/admin/login`, 303)
}
