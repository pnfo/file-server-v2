import assert from 'node:assert/strict'
import { createHmac } from 'node:crypto'
import { test } from 'node:test'
import { buildFileName, parseFileName, saveAsName } from '../src/lib/naming.ts'
import { sanitizeHtml } from '../src/lib/sanitize.ts'
import { LINK_WINDOW_SECONDS, signLink, windowEnd } from '../src/lib/sign.ts'
import { entriesFromListing } from '../src/lib/sync.ts'
import { chromeIntent, detectBrowser } from '../src/lib/ua.ts'

test('parseFileName follows name[version]{id}.type', () => {
  assert.deepEqual(parseFileName('නාමමාලා - වස්කඩුවේ සුභූති හිමි[1965]{200}.pdf'), { name: 'නාමමාලා - වස්කඩුවේ සුභූති හිමි', version: '1965', id: 200, type: 'pdf' })
  assert.deepEqual(parseFileName('අභිධර්ම{1}'), { name: 'අභිධර්ම', version: null, id: 1, type: null })
  assert.equal(parseFileName('-dev').id, 0)
  assert.equal(buildFileName('a/b', 'x]y', 5, 'pdf'), 'a b[x y]{5}.pdf')
  assert.equal(saveAsName({ name: 'පොත', version: '1965', type: 'pdf' }), 'පොත - 1965.pdf')
})

test('link windows: identical URL within a window, 6-12 h lifetime', () => {
  const t = Date.UTC(2026, 9, 1, 7, 30) // 07:30 UTC
  const e = windowEnd(t)
  assert.equal(e, Date.UTC(2026, 9, 1, 18) / 1000)
  assert.ok(e - t / 1000 >= LINK_WINDOW_SECONDS && e - t / 1000 <= 2 * LINK_WINDOW_SECONDS)
  assert.equal(windowEnd(t + 3600_000), e)
})

test('signLink produces a SigV2 query signature over path, expiry and disposition', () => {
  const nowMs = Date.UTC(2026, 9, 1, 0, 0)
  const { url, expires } = signLink({ objectKey: 'library/අ{1}/x[v]{2}.pdf', fileName: 'x - v.pdf', mode: 'inline', accessKeyId: 'AK', secretAccessKey: 'SECRET', nowMs })
  const u = new URL(url)
  assert.equal(u.host, 'tipitaka.sgp1.cdn.digitaloceanspaces.com')
  assert.equal(decodeURIComponent(u.pathname), '/library/අ{1}/x[v]{2}.pdf')
  assert.equal(u.searchParams.get('Expires'), String(expires))
  const cd = u.searchParams.get('response-content-disposition')!
  assert.equal(cd, `inline; filename="file.pdf"; filename*=UTF-8''x%20-%20v.pdf`)
  const sts = `GET\n\n\n${expires}\n/tipitaka${u.pathname}?response-content-disposition=${cd}`
  assert.equal(u.searchParams.get('Signature'), createHmac('sha1', 'SECRET').update(sts).digest('base64'))
})

test('entriesFromListing builds folders, skips duplicates, marks id-less folders unlisted', () => {
  const o = (key: string, size = 10) => ({ key: `library/${key}`, size })
  const { files, folders, skipped } = entriesFromListing([
    o('අභිධර්ම{1}/පොත[1965]{2}.pdf'), o('අභිධර්ම{1}/sub{3}/b{4}.mp3'), o('thumbs/2-0.jpg'),
    o('-dev/test{7}/c{8}.pdf'), o('dup{2}.pdf'), o('noid.pdf'),
  ], 'library')
  assert.deepEqual([...folders.keys()].sort(), [1, 3, 7])
  assert.equal(folders.get(7)!.parentId, -1)
  assert.equal(files.get(2)!.version, '1965')
  assert.equal(files.get(4)!.parentId, 3)
  assert.equal(files.get(8)!.parentId, 7)
  assert.deepEqual(skipped.map(s => s.key), ['dup{2}.pdf', 'noid.pdf'])
})

test('sanitizeHtml keeps simple formatting only', () => {
  assert.equal(sanitizeHtml('<p onclick="x">a <b>b</b><script>alert(1)</script></p>'), '<p>a <b>b</b></p>')
  assert.equal(sanitizeHtml('<a href="javascript:alert(1)">x</a>'), '<a>x</a>')
  assert.equal(sanitizeHtml('<a href="https://x.lk/a">x</a>'), '<a href="https://x.lk/a" rel="nofollow noopener">x</a>')
  assert.equal(sanitizeHtml('<p><br></p>'), null)
})

test('in-app browser detection', () => {
  const fbAndroid = 'Mozilla/5.0 (Linux; Android 14; SM-A146P Build/UP1A; wv) AppleWebKit/537.36 Chrome/128.0 Mobile Safari/537.36 [FB_IAB/FB4A;FBAV/480.0.0.48.86;]'
  assert.deepEqual(detectBrowser(fbAndroid), { inApp: true, appName: 'Facebook', os: 'android' })
  assert.equal(detectBrowser('Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Instagram 350.0').appName, 'Instagram')
  assert.equal(detectBrowser('Mozilla/5.0 (Linux; Android 14) Chrome/128.0 Mobile Safari/537.36').inApp, false)
  assert.match(chromeIntent('https://tipitaka.lk/library/2'), /^intent:\/\/tipitaka\.lk\/library\/2#Intent;scheme=https;package=com\.android\.chrome;/)
})
