// End-to-end admin test: drives the admin UI in headless Chrome against a local server and the REAL bucket.
//
//   npm run test:e2e
//
// Needs: config/secrets.json, Google Chrome (or CHROME=/path/to/chrome), and the bucket CORS rule allowing
// http://localhost:4321 (admin -> Bucket -> Set CORS rule). Starts the server (server/bin/file_server, built by
// `npm run build`) on :4321 with this repo's config/ and data/ unless one is already running there.
// Everything happens inside a new "fs-v2 e2e" folder in the library site, which is deleted at the end
// (also after a failure). Leftovers from an interrupted run are removed before starting.
import { spawn } from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { DatabaseSync } from 'node:sqlite'

const APP = path.resolve(path.dirname(new URL(import.meta.url).pathname), '../..')
process.chdir(APP)
const B = 'http://localhost:4321'
const ROOT = 'library'
const CDN = `https://tipitaka.sgp1.cdn.digitaloceanspaces.com/${ROOT}`
const PREFIX = 'fs-v2 e2e'
const PW = JSON.parse(fs.readFileSync('config/secrets.json', 'utf-8')).adminPassword
const s3 = await import('./s3.mjs')
const TMP = fs.mkdtempSync(path.join(os.tmpdir(), 'fs-e2e-'))
const sleep = ms => new Promise(r => setTimeout(r, ms))

// ---- fixtures -------------------------------------------------------------------------------
/** A small valid PDF with `pages` pages of Helvetica text. */
function makePdf(pages) {
  const objs = []
  const add = body => { objs.push(body); return objs.length }
  const catalog = add(''), tree = add(''), font = add('<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>')
  const kids = []
  for (let p = 1; p <= pages; p++) {
    const text = `BT /F1 64 Tf 150 450 Td (Page ${p}) Tj ET`
    const content = add(`<< /Length ${text.length} >>\nstream\n${text}\nendstream`)
    kids.push(add(`<< /Type /Page /Parent ${tree} 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 ${font} 0 R >> >> /Contents ${content} 0 R >>`))
  }
  objs[catalog - 1] = `<< /Type /Catalog /Pages ${tree} 0 R >>`
  objs[tree - 1] = `<< /Type /Pages /Kids [${kids.map(k => `${k} 0 R`).join(' ')}] /Count ${pages} >>`
  let out = '%PDF-1.4\n'
  const offsets = objs.map((body, i) => { const o = out.length; out += `${i + 1} 0 obj\n${body}\nendobj\n`; return o })
  const xref = out.length
  out += `xref\n0 ${objs.length + 1}\n0000000000 65535 f \n${offsets.map(o => `${String(o).padStart(10, '0')} 00000 n \n`).join('')}`
  out += `trailer\n<< /Size ${objs.length + 1} /Root ${catalog} 0 R >>\nstartxref\n${xref}\n%%EOF\n`
  return Buffer.from(out, 'latin1')
}
const pdfFile = path.join(TMP, `${PREFIX} book[scanned].pdf`)
const txtFile = path.join(TMP, `${PREFIX} notes.txt`)
fs.writeFileSync(pdfFile, makePdf(5))
fs.writeFileSync(txtFile, `fs-v2 e2e text file ${new Date().toISOString()}\n`)

// ---- helpers --------------------------------------------------------------------------------
const db = new DatabaseSync(path.join(APP, 'data', `${ROOT}.db`), { readOnly: true })
const row = id => db.prepare('SELECT * FROM entries WHERE id = ?').get(id)
let failures = 0
const check = (ok, msg) => { console.log(`${ok ? '  ok  ' : '  FAIL'} ${msg}`); if (!ok) failures++ }
const status = async url => (await fetch(url, { redirect: 'manual', headers: { Range: 'bytes=0-0' } })).status
const up = async () => { try { return (await fetch(`${B}/${ROOT}/api/health`)).status === 200 } catch { return false } }

async function api(cookie, action, body) {
  const r = await fetch(`${B}/${ROOT}/admin/api/${action}`, { method: 'POST', headers: { 'Content-Type': 'application/json', 'x-fs-admin': '1', cookie }, body: JSON.stringify(body) })
  return r.json()
}
async function login() {
  const r = await fetch(`${B}/${ROOT}/admin/login`, { method: 'POST', redirect: 'manual', body: new URLSearchParams({ password: PW }) })
  return (r.headers.get('set-cookie') || '').split(';')[0]
}
/** Delete every e2e entry (files first, deepest folders first) through the admin API. */
async function cleanup() {
  const cookie = await login()
  const ids = db.prepare(`SELECT id FROM entries WHERE name LIKE ? ORDER BY kind = 'folder', id DESC`).all(`${PREFIX}%`).map(r => r.id)
  for (const id of ids) await api(cookie, 'delete', { id })
  return ids.length
}

// ---- server + Chrome ------------------------------------------------------------------------
let devServer
if (!(await up())) {
  const bin = path.join(APP, 'server/bin/file_server')
  if (!fs.existsSync(bin)) { console.error('server/bin/file_server not found - run npm run build'); process.exit(1) }
  console.log('starting the server on :4321')
  devServer = spawn(bin, ['serve'], { cwd: APP, stdio: 'ignore', detached: true, env: { ...process.env, PORT: '4321', HOST: 'localhost', LOG_REQUESTS: '0' } })
  for (let i = 0; i < 30 && !(await up()); i++) await sleep(500)
  if (!(await up())) { console.error('server did not start'); process.exit(1) }
}
const chromePath = process.env.CHROME || ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', '/usr/bin/google-chrome', '/usr/bin/chromium'].find(p => fs.existsSync(p))
if (!chromePath) { console.error('Chrome not found - set CHROME=/path/to/chrome'); process.exit(1) }
const profile = path.join(TMP, 'chrome')
const chrome = spawn(chromePath, ['--headless=new', '--disable-gpu', '--no-first-run', '--remote-debugging-port=0', `--user-data-dir=${profile}`, 'about:blank'], { stdio: 'ignore' })
let port
for (let i = 0; i < 50 && !port; i++) { try { port = fs.readFileSync(path.join(profile, 'DevToolsActivePort'), 'utf-8').split('\n')[0] } catch { await sleep(200) } }
const tabs = await (await fetch(`http://127.0.0.1:${port}/json`)).json()
const ws = new WebSocket(tabs.find(t => t.type === 'page').webSocketDebuggerUrl)
await new Promise(r => (ws.onopen = r))

let n = 0
const pending = new Map(), dialogs = [], pageErrors = []
const send = (method, params = {}) => new Promise(r => { const id = ++n; pending.set(id, r); ws.send(JSON.stringify({ id, method, params })) })
ws.onmessage = m => {
  const d = JSON.parse(m.data)
  if (d.method === 'Page.javascriptDialogOpening') send('Page.handleJavaScriptDialog', { accept: true, promptText: dialogs.shift() ?? '' })
  if (d.method === 'Runtime.exceptionThrown') pageErrors.push(d.params.exceptionDetails.exception?.description || d.params.exceptionDetails.text)
  pending.get(d.id)?.(d); pending.delete(d.id)
}
const ev = async expr => {
  const r = await send('Runtime.evaluate', { expression: expr, awaitPromise: true, returnByValue: true })
  if (r.result?.exceptionDetails) throw new Error(`in page: ${expr.slice(0, 80)}: ${r.result.exceptionDetails.exception?.description}`)
  return r.result?.result?.value
}
async function waitFor(expr, timeout = 30000) {
  const t0 = Date.now()
  while (Date.now() - t0 < timeout) { try { if (await ev(expr)) return } catch {} await sleep(250) }
  throw new Error(`timed out waiting for: ${expr}`)
}
// admin pages set body[data-ready] once their script has attached its handlers
const ready = `document.readyState === 'complete' && (!location.pathname.includes('/admin') || location.pathname.endsWith('/login') || document.body.dataset.ready === '1')`
const go = async url => { await send('Page.navigate', { url: B + url }); await waitFor(ready) }
const click = sel => ev(`document.querySelector(${JSON.stringify(sel)}).click()`)
const clickReload = async sel => { await ev('window.__old = 1'); await click(sel); await waitFor(`!window.__old && ${ready}`) }
const setValue = (sel, v) => ev(`(() => { const e = document.querySelector(${JSON.stringify(sel)}); e.value = ${JSON.stringify(v)}; e.dispatchEvent(new Event('input', { bubbles: true })) })()`)
const submitAndWaitSaved = async () => { await ev(`document.querySelector('#save-status').textContent = ''; document.querySelector('#meta-form').requestSubmit()`); await waitFor(`/Saved/.test(document.querySelector('#save-status').textContent)`) }
const toast = () => ev(`document.querySelector('#toast')?.textContent || ''`)
await send('Page.enable'); await send('Runtime.enable'); await send('DOM.enable')
await send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 1000, deviceScaleFactor: 1, mobile: false })

// ---- the test -------------------------------------------------------------------------------
try {
  const stale = await cleanup()
  if (stale) console.log(`removed ${stale} leftover e2e entries`)

  console.log('login')
  await go(`/${ROOT}/admin/login`)
  await ev(`document.querySelector('#pw').value = ${JSON.stringify(PW)}; document.querySelector('form').submit()`)
  await waitFor(`location.pathname === '/${ROOT}/admin' && ${ready}`)
  check(true, 'logged in')

  console.log('create folder')
  dialogs.push(`${PREFIX} folder`)
  await click('[data-new-folder]')
  await waitFor(`location.search.startsWith('?folder=') && ${ready}`)
  const folderId = Number(await ev(`new URLSearchParams(location.search).get('folder')`))
  const folder = row(folderId)
  check(folder?.kind === 'folder' && folder.parent_id === 0, `folder #${folderId} "${folder?.s3_key}"`)
  check((await fetch(`${B}/${ROOT}/${folderId}`)).status === 404, 'empty folder is hidden on the public site')

  console.log('upload')
  await go(`/${ROOT}/admin/upload?folder=${folderId}`)
  check(await ev(`document.querySelector('#upload-folder').value`) === String(folderId), 'upload page preselects the folder')
  const doc = await send('DOM.getDocument')
  const input = await send('DOM.querySelector', { nodeId: doc.result.root.nodeId, selector: '#upload-input' })
  await send('DOM.setFileInputFiles', { nodeId: input.result.nodeId, files: [pdfFile, txtFile] })
  await waitFor(`document.querySelectorAll('#upload-list > div').length === 2`)
  check(await ev(`document.querySelector('#upload-list [data-k=name]').value`) === `${PREFIX} book`, 'name parsed from the file name')
  check(await ev(`document.querySelector('#upload-list [data-k=version]').value`) === 'scanned', 'version parsed from [..]')
  await setValue('#upload-list [data-k=author]', 'Test Author')
  await setValue('#upload-list [data-k=publishingYear]', '1965')
  await click('#upload-start')
  await waitFor(`[...document.querySelectorAll('[data-msg]')].every(m => /Uploaded as/.test(m.textContent) || m.classList.contains('!text-rose-500'))`, 120000)
  await waitFor(`/done \\(|previews failed/.test(document.querySelector('[data-msg]').textContent)`, 120000)
  for (const m of await ev(`[...document.querySelectorAll('[data-msg]')].map(m => m.textContent)`)) console.log(`        ${m}`)
  const [pdfId, txtId] = await ev(`[...document.querySelectorAll('[data-msg] a')].map(a => Number(a.textContent.slice(1)))`)
  const pdf = row(pdfId), txt = row(txtId)
  check(pdf?.parent_id === folderId && pdf.author === 'Test Author' && pdf.publishing_year === 1965 && pdf.version === 'scanned', `pdf #${pdfId} saved with metadata`)
  check(pdf?.s3_key === `${folder.s3_key}/${PREFIX} book[scanned]{${pdfId}}.pdf`, `key follows name[version]{id}.type`)
  check(await s3.exists(`${ROOT}/${pdf.s3_key}`), 'pdf is in the bucket')
  check(txt && await s3.exists(`${ROOT}/${txt.s3_key}`), `txt #${txtId} saved and in the bucket`)
  check(pdf.num_pages === 5 && pdf.preview_pages === '[1,3]', `default previews: pages ${pdf.preview_pages} of ${pdf.num_pages}`)
  for (const i of [0, 1]) check(await status(`${CDN}/previews/${pdfId}-${i}.jpg`) < 300, `preview image ${i} is public`)

  console.log('public pages')
  await go(`/${ROOT}/${folderId}`)
  check(await ev(`document.querySelectorAll('tbody tr').length`) === 2, 'folder page lists both files')
  await go(`/${ROOT}/${pdfId}`)
  check(await ev(`document.querySelectorAll('[data-previews] img').length`) === 2, 'file page shows both previews')
  check(await status((await ev(`document.querySelector('a[data-hit][data-mode=inline]').href`)) + '&fbclid=x') === 206, 'view link works with fbclid appended')
  check((await fetch(`${B}/${ROOT}/${pdfId}/all`, { redirect: 'manual' })).headers.get('location') === `/${ROOT}/${pdfId}`, '/<id>/all redirects to /<id>')

  console.log('edit file (renames the object)')
  await go(`/${ROOT}/admin/edit/${pdfId}`)
  await setValue('#f-name', `${PREFIX} renamed book`)
  await setValue('#f-version', 'v2')
  await setValue('#f-author', 'Another Author')
  await ev(`document.querySelector('#description').innerHTML = '<p>Test <b>description</b> <script>alert(1)</script></p>'`)
  await submitAndWaitSaved()
  const renamed = row(pdfId)
  check(renamed.s3_key === `${folder.s3_key}/${PREFIX} renamed book[v2]{${pdfId}}.pdf`, `renamed key "${renamed.s3_key}"`)
  check(await s3.exists(`${ROOT}/${renamed.s3_key}`) && !(await s3.exists(`${ROOT}/${pdf.s3_key}`)), 'object moved: new key exists, old key gone')
  check(renamed.is_published === 1, 'saving the form keeps it published')
  check(renamed.description === '<p>Test <b>description</b> </p>', 'description is sanitised')
  check(renamed.updated_at.slice(0, 10) === new Date().toISOString().slice(0, 10), 'updated date is set by the edit')
  await go(`/${ROOT}/${pdfId}`)
  check(await ev(`document.querySelector('h1')?.textContent.includes('renamed book')`), 'public page shows the new name')
  check(await status(await ev(`document.querySelector('a[data-hit][data-mode=attachment]').href`)) === 206, 'download link works after the rename')

  console.log('previews from the edit page (pdf.js over CORS)')
  await go(`/${ROOT}/admin/edit/${pdfId}`)
  await setValue('#f-pages', '2')
  await click('[data-render]')
  await waitFor(`/Rendered/.test(document.querySelector('#pdf-status').textContent)`, 60000)
  check(await ev(`document.querySelectorAll('#preview-canvases canvas').length`) === 1, 'page rendered in the browser')
  await click('[data-save-previews]')
  await waitFor(`/Previews saved/.test(document.querySelector('#pdf-status').textContent)`, 60000)
  check(row(pdfId).preview_pages === '[2]', 'preview pages saved as [2]')
  check(!(await s3.exists(`${ROOT}/previews/${pdfId}-1.jpg`)), 'the no-longer-used second preview image was removed')
  await go(`/${ROOT}/${pdfId}`)
  check(await ev(`document.querySelector('[data-previews] figcaption')?.textContent.includes('2')`), 'public page shows the page 2 preview')

  console.log('publish / unpublish')
  await go(`/${ROOT}/admin/edit/${txtId}`)
  await clickReload('[data-publish]')
  check(row(txtId).is_published === 0 && (await fetch(`${B}/${ROOT}/${txtId}`)).status === 404, 'unpublished file: public page 404')
  await clickReload('[data-publish]')
  check(row(txtId).is_published === 1 && (await fetch(`${B}/${ROOT}/${txtId}`)).status === 200, 'republished file: public page 200')
  await go(`/${ROOT}/admin/edit/${folderId}`)
  await clickReload('[data-publish]')
  check((await fetch(`${B}/${ROOT}/${folderId}`)).status === 404 && (await fetch(`${B}/${ROOT}/${pdfId}`)).status === 404, 'unpublished folder hides itself and its files')
  await clickReload('[data-publish]')
  check((await fetch(`${B}/${ROOT}/${pdfId}`)).status === 200, 'republished folder: files visible again')

  console.log('folders')
  await go(`/${ROOT}/admin/edit/${folderId}`)
  await setValue('#f-name', `${PREFIX} folder renamed`)
  await ev(`document.querySelector('#meta-form').requestSubmit()`)
  await waitFor(`/cannot be renamed/.test(document.querySelector('#toast').textContent)`)
  check(row(folderId).name === `${PREFIX} folder`, 'renaming a folder that has files is refused')
  await go(`/${ROOT}/admin?folder=${folderId}`)
  dialogs.push(`${PREFIX} sub folder`)
  await click('[data-new-folder]')
  await waitFor(`location.search !== '?folder=${folderId}' && ${ready}`)
  const subId = Number(await ev(`new URLSearchParams(location.search).get('folder')`))
  check(row(subId)?.parent_id === folderId, `subfolder #${subId} created inside the test folder`)
  await go(`/${ROOT}/admin/edit/${subId}`)
  await setValue('#f-name', `${PREFIX} sub folder renamed`)
  await submitAndWaitSaved()
  check(row(subId).s3_key.endsWith(`${PREFIX} sub folder renamed{${subId}}`), 'empty folder renamed')
  dialogs.push('')
  await click('[data-delete]')
  await waitFor(`location.pathname === '/${ROOT}/admin' && ${ready}`)
  check(!row(subId), 'empty subfolder deleted')

  console.log('delete')
  await go(`/${ROOT}/admin/edit/${folderId}`)
  check(await ev(`document.querySelector('[data-delete]').disabled`), 'delete is disabled while the folder has files')
  for (const id of [pdfId, txtId]) {
    const key = row(id).s3_key
    await go(`/${ROOT}/admin/edit/${id}`)
    dialogs.push('')
    await click('[data-delete]')
    await waitFor(`location.pathname === '/${ROOT}/admin' && ${ready}`)
    check(!row(id) && !(await s3.exists(`${ROOT}/${key}`)), `#${id} deleted from the database and the bucket`)
  }
  const leftPreviews = []
  for (let i = 0; i < 6; i++) if (await s3.exists(`${ROOT}/previews/${pdfId}-${i}.jpg`)) leftPreviews.push(i)
  check(!leftPreviews.length, `all preview images deleted${leftPreviews.length ? ` (left: ${leftPreviews})` : ''}`)
  await go(`/${ROOT}/admin/edit/${folderId}`)
  dialogs.push('')
  await click('[data-delete]')
  await waitFor(`location.pathname === '/${ROOT}/admin' && ${ready}`)
  check(!row(folderId), 'test folder deleted')
  check((await s3.listAll(`${ROOT}/${folder.s3_key}`)).length === 0, 'nothing left in the bucket under the test folder')
} catch (err) {
  failures++
  console.log('  FAIL', err.message)
  const shot = await send('Page.captureScreenshot', {})
  const file = path.join(os.tmpdir(), 'fs-e2e-failure.png')
  fs.writeFileSync(file, Buffer.from(shot.result.data, 'base64'))
  console.log(`        screenshot: ${file}   toast: "${await toast().catch(() => '')}"`)
} finally {
  const left = await cleanup().catch(() => -1)
  if (left) console.log(left > 0 ? `cleaned up ${left} test entries` : 'cleanup failed - remove "fs-v2 e2e" entries in the admin')
  if (pageErrors.length) console.log('page errors:', pageErrors)
  ws.close()
  await new Promise(r => { chrome.once('exit', r); chrome.kill(); setTimeout(r, 3000) })
  if (devServer) process.kill(-devServer.pid)
  try { fs.rmSync(TMP, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 }) } catch {} // Chrome may still be flushing its profile
}
console.log(failures ? `\n${failures} FAILED` : '\nALL PASSED')
process.exit(failures ? 1 : 0)
