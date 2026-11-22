// node worker/test.mjs - the Worker's link check against the vectors the Go signer is tested with.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import worker, { verify, isPublic, isForbidden, canonicalPath, keyOf } from './src/index.ts'

const v = JSON.parse(readFileSync(new URL('../server/testdata/link_vectors.json', import.meta.url), 'utf8'))
const now = Math.floor(v.nowMs / 1000)
let n = 0
const check = async (name, url, want) => {
  const got = await verify(new URL(url), v.secret, now)
  assert.equal(got.ok ? 200 : got.status, want, `${name}: ${url} -> ${JSON.stringify(got)}`)
  n++
  return got
}

for (const l of v.links) {
  const u = l.out.url
  const got = await check('signed by Go', u, 200)
  assert.equal(got.key, l.objectKey)
  assert.equal(got.disposition, new URL(u).searchParams.get('response-content-disposition'))
  await check('fbclid appended', u + '&fbclid=IwAR0abc&utm_source=fb', 200)
  await check('expired', u, 200) // still inside its window at nowMs ...
  assert.equal((await verify(new URL(u), v.secret, l.out.expires + 1)).ok, false, 'expired link accepted') // ... not after it
  await check('wrong secret', u.replace(/Signature=./, m => m.slice(0, -1) + (m.endsWith('A') ? 'B' : 'A')), 403)
  await check('later expiry', u.replace(/Expires=\d+/, 'Expires=' + (l.out.expires + 86400)), 403)
  await check('no signature', u.split('?')[0], 403)
  if (l.mode !== 'stream') await check('other filename', u.replace(/response-content-disposition=[^&]*/, 'response-content-disposition=inline'), 403)
  else await check('disposition added', u + '&response-content-disposition=attachment', 403)
  // a client that keeps characters unescaped, or lower-cases the escapes, still verifies
  const [path, query] = u.split('?')
  const { pathname, origin } = new URL(path)
  await check('lower-case escapes', origin + pathname.replace(/%[0-9A-F]{2}/g, m => m.toLowerCase()) + '?' + query, 200)
  await check('unescaped path', origin + '/' + keyOf(pathname).split('/').map(s => encodeURI(s).replace(/[?#]/g, encodeURIComponent)).join('/') + '?' + query, 200)
  // a signature for one object does not open another
  await check('other object', origin + pathname + 'x?' + query, 403)
}

const host = 'https://' + v.host
await check('preview is public', host + '/library/previews/12-0.jpg?v=2026', 200)
await check('thumb is public', host + '/library/thumbs/12-1.jpg', 200)
await check('nested preview is not', host + '/library/previews/a/b.jpg', 403)
await check('folder named previews deeper down is not', host + '/library/x%7B1%7D/previews/b.pdf', 403)
await check('db snapshot', host + '/library/backups/library-2026-11-01.db', 403)
await check('cloud db snapshot', host + '/cloud-db-backups/cloud-2026-11-01.db', 403)
await check('bad escape', host + '/library/%E0%A4%A', 404)
assert.ok(isForbidden('library/backups/replace/x.pdf') && isForbidden('cloud-db-backups/x.db') && !isForbidden('library/a{1}/backups.pdf'))
assert.ok(isPublic('library/previews/1-0.jpg') && !isPublic('previews/1-0.jpg'))
assert.equal(canonicalPath("a b/c(1)!*'.mp3"), '/a%20b/c%281%29%21%2A%27.mp3')

// the fetch handler against an in-memory bucket
const body = new TextEncoder().encode('0123456789')
const obj = (range) => {
  const o = { size: body.length, httpEtag: '"abc"', uploaded: new Date(0), httpMetadata: { contentType: 'application/pdf' } }
  if (range === null) return o
  let [start, end] = [0, body.length - 1]
  if (range) {
    const m = /^bytes=(\d*)-(\d*)$/.exec(range)
    if (m[1] === '') { start = Math.max(0, body.length - Number(m[2])); o.range = { suffix: Number(m[2]) } }
    else { start = Number(m[1]); end = m[2] === '' ? end : Math.min(end, Number(m[2])); o.range = { offset: start, length: end - start + 1 } }
    if (start >= body.length) throw new Error('InvalidRange')
  }
  o.body = new Blob([body.slice(start, end + 1)]).stream()
  return o
}
const signed = v.links[0]
const env = {
  LINK_SECRET: v.secret,
  BUCKET: {
    head: async key => (key === signed.objectKey ? obj(null) : null),
    get: async (key, opt) => {
      if (key !== signed.objectKey) return null
      if (opt.onlyIf.get('If-None-Match') === '"abc"') return obj(null)
      return obj(opt.range.get('Range') ?? undefined)
    },
  },
}
const realNow = Date.now
Date.now = () => v.nowMs
const call = (url, init) => worker.fetch(new Request(url, init), env)
let r = await call(signed.out.url)
assert.equal(r.status, 200); assert.equal(await r.text(), '0123456789')
assert.equal(r.headers.get('Content-Type'), 'application/pdf'); assert.equal(r.headers.get('Content-Length'), '10')
assert.match(r.headers.get('Content-Disposition'), /^inline; filename="file.pdf"; filename\*=UTF-8''/)
r = await call(signed.out.url, { headers: { Range: 'bytes=0-0' } })
assert.equal(r.status, 206); assert.equal(r.headers.get('Content-Range'), 'bytes 0-0/10'); assert.equal(await r.text(), '0')
r = await call(signed.out.url, { headers: { Range: 'bytes=4-' } })
assert.equal(r.status, 206); assert.equal(r.headers.get('Content-Range'), 'bytes 4-9/10'); assert.equal(await r.text(), '456789')
r = await call(signed.out.url, { headers: { Range: 'bytes=-3' } })
assert.equal(r.status, 206); assert.equal(r.headers.get('Content-Range'), 'bytes 7-9/10'); assert.equal(await r.text(), '789')
r = await call(signed.out.url, { headers: { Range: 'bytes=50-' } })
assert.equal(r.status, 416); assert.equal(r.headers.get('Content-Range'), 'bytes */10')
r = await call(signed.out.url, { headers: { 'If-None-Match': '"abc"' } })
assert.equal(r.status, 304)
r = await call(signed.out.url, { method: 'HEAD' })
assert.equal(r.status, 200); assert.equal(r.headers.get('Content-Length'), '10'); assert.equal(r.headers.get('Accept-Ranges'), 'bytes')
assert.equal((await call(signed.out.url, { method: 'POST' })).status, 405)
assert.equal((await call(signed.out.url.split('?')[0])).status, 403)
assert.equal((await call(host + '/library/previews/9-0.jpg')).status, 404) // public, but not in the test bucket
r = await call(host + '/', { redirect: 'manual' })
assert.equal(r.status, 302); assert.equal(r.headers.get('Location'), 'https://tipitaka.lk/library')
Date.now = realNow
console.log(`worker: ${n} link checks and the fetch handler passed`)
