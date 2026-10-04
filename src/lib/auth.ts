import { createHmac, timingSafeEqual } from 'node:crypto'
import { getSecrets } from './config.ts'

export const COOKIE = 'fs_admin'
const TTL = 7 * 86400

const mac = (payload: string) => createHmac('sha256', getSecrets().sessionSecret).update(payload).digest('base64url')
const safeEqual = (a: string, b: string) => {
  const x = Buffer.from(a), y = Buffer.from(b)
  return x.length === y.length && timingSafeEqual(x, y)
}

export function createSession() {
  const exp = Math.floor(Date.now() / 1000) + TTL
  return { value: `${exp}.${mac(String(exp))}`, maxAge: TTL }
}
export function verifySession(value: string | undefined) {
  if (!value) return false
  const [exp, sig] = value.split('.')
  return !!sig && Number(exp) > Date.now() / 1000 && safeEqual(sig, mac(exp))
}
export const checkPassword = (pw: string) => safeEqual(mac(pw), mac(getSecrets().adminPassword))

// naive per-IP limiter for login attempts
const attempts = new Map<string, { n: number; until: number }>()
export function loginAllowed(ip: string) {
  const a = attempts.get(ip)
  return !a || a.n < 5 || a.until < Date.now()
}
export function recordLogin(ip: string, ok: boolean) {
  if (ok) { attempts.delete(ip); return }
  const a = attempts.get(ip)
  const n = a && a.until > Date.now() ? a.n + 1 : 1
  attempts.set(ip, { n, until: Date.now() + 15 * 60_000 })
}
