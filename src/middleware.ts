import { defineMiddleware } from 'astro:middleware'
import { COOKIE, verifySession } from './lib/auth.ts'
import { getSiteConfig, resolvePrefix } from './lib/config.ts'
import { getStore } from './lib/store.ts'

export const onRequest = defineMiddleware(async (ctx, next) => {
  const started = Date.now()
  const [, prefix = '', ...rest] = ctx.url.pathname.split('/')
  const r = resolvePrefix(prefix)
  if (!r) return next()

  ctx.locals.site = { id: r.site, base: `/${prefix}`, isAlias: r.isAlias, cfg: getSiteConfig(r.site), store: getStore(r.site) }

  if (rest[0] === 'admin') {
    const isAdmin = verifySession(ctx.cookies.get(COOKIE)?.value)
    ctx.locals.isAdmin = isAdmin
    const isLogin = rest[1] === 'login'
    if (!isAdmin && !isLogin) {
      return rest[1] === 'api'
        ? new Response(JSON.stringify({ error: 'login required' }), { status: 401, headers: { 'Content-Type': 'application/json' } })
        : ctx.redirect(`/${prefix}/admin/login`)
    }
    // CSRF: admin API writes must come from our own scripts (a custom header can't be sent cross-site without CORS)
    if (rest[1] === 'api' && ctx.request.method !== 'GET' && ctx.request.headers.get('x-fs-admin') !== '1') {
      return new Response('Forbidden', { status: 403 })
    }
  }

  const res = await next()
  if (r.isAlias) res.headers.set('X-Robots-Tag', 'noindex')
  if (rest[0] === 'admin') res.headers.set('Cache-Control', 'no-store')
  if (process.env.LOG_REQUESTS !== '0' && rest[0] !== 'api') {
    console.log(`${ctx.request.method} ${decodeURIComponent(ctx.url.pathname)} ${res.status} ${Date.now() - started}ms`)
  }
  return res
})
