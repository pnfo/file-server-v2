import type { SiteConfig, SiteId } from './config.ts'
import type { SiteStore } from './store.ts'

export interface SiteContext {
  id: SiteId
  base: string        // URL prefix this request came in on, e.g. "/library" or "/library-v2"
  isAlias: boolean
  cfg: SiteConfig
  store: SiteStore
}

export const json = (body: unknown, status = 200, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store', ...headers } })

export function clientKey(request: Request, clientAddress?: string) {
  return request.headers.get('x-real-ip') || request.headers.get('x-forwarded-for')?.split(',')[0].trim() || clientAddress || 'unknown'
}

export const parseId = (s: string | undefined) => (s && /^\d+$/.test(s) ? Number(s) : NaN)
