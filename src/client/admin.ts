// Admin page behaviour: JSON calls, uploads straight to Spaces, and pdf.js previews rendered in this browser.
import { initLightbox } from './lightbox.ts'

const base = document.body.dataset.base || ''
initLightbox({ close: 'Close', prev: 'Previous', next: 'Next' })
const $ = <T extends Element = HTMLElement>(sel: string, root: ParentNode = document) => root.querySelector<T>(sel)

async function api<T = any>(action: string, body: object): Promise<T> {
  const res = await fetch(`${base}/admin/api/${action}`, {
    method: 'POST', headers: { 'Content-Type': 'application/json', 'x-fs-admin': '1' }, body: JSON.stringify(body),
  })
  const data = await res.json().catch(() => ({ error: res.statusText }))
  if (!res.ok) throw new Error(data.error || res.statusText)
  return data
}
function toast(msg: string, isError = false) {
  const t = $('#toast')!
  t.textContent = msg
  t.classList.toggle('!bg-rose-600', isError)
  t.classList.toggle('!text-white', isError)
  t.classList.remove('opacity-0')
  setTimeout(() => t.classList.add('opacity-0'), isError ? 6000 : 2500)
}
const report = (data: unknown) => { const r = $('#report'); if (r) { r.classList.remove('hidden'); r.textContent = JSON.stringify(data, null, 2) } }

// ---- buttons: theme, publish, delete, new folder, sync, cors -----------------------------------
document.addEventListener('click', async ev => {
  const t = ev.target as HTMLElement
  if (t.closest('[data-theme-toggle]')) {
    const dark = document.documentElement.classList.toggle('dark')
    try { localStorage.setItem('fs-theme', dark ? 'dark' : 'light') } catch {}
    return
  }
  const el = t.closest<HTMLElement>('[data-publish],[data-delete],[data-new-folder],[data-sync],[data-cors]')
  if (!el || el.hasAttribute('disabled')) return
  try {
    if (el.dataset.publish) {
      el.setAttribute('disabled', '')
      await api('publish', { id: el.dataset.publish, isPublished: el.dataset.value === '1' })
      location.reload()
    } else if (el.dataset.delete) {
      if (!confirm(`Permanently delete "${el.dataset.name}" (#${el.dataset.delete})? This cannot be undone.`)) return
      el.setAttribute('disabled', '')
      await api('delete', { id: el.dataset.delete })
      location.href = `${base}/admin${el.dataset.parent && el.dataset.parent !== '0' ? `?folder=${el.dataset.parent}` : ''}`
    } else if (el.dataset.newFolder !== undefined) {
      const name = prompt('New folder name')
      if (!name) return
      const f = await api('folder', { parentId: el.dataset.newFolder, name })
      toast('Folder created. It shows on the public site once it has files.')
      location.href = `${base}/admin?folder=${f.id}`
    } else if (el.dataset.sync) {
      el.setAttribute('disabled', '')
      const r = await api('sync', { dryRun: el.dataset.sync === 'dry' })
      report(r)
      toast(`${r.dryRun ? 'Dry run: ' : ''}${r.added.length} added, ${r.updated.length} updated, ${r.missing.length} missing`)
    } else if (el.dataset.cors) {
      if (el.dataset.cors === 'set' && !confirm('Replace the bucket CORS rules to allow GET/PUT from this site? (Needed for browser uploads and previews.)')) return
      el.setAttribute('disabled', '')
      report(await api(el.dataset.cors === 'set' ? 'cors-set' : 'cors-get', {}))
    }
  } catch (err) {
    toast((err as Error).message, true)
  }
  if (!el.dataset.publish && !el.dataset.delete) el.removeAttribute('disabled')
})

// ---- edit page: metadata -----------------------------------------------------------------------
const metaForm = $<HTMLFormElement>('#meta-form')
if (metaForm) {
  const desc = $('#description')
  $('[data-rte-toolbar]')?.addEventListener('click', e => {
    const cmd = (e.target as HTMLElement).closest<HTMLElement>('[data-cmd]')?.dataset.cmd
    if (!cmd) return
    desc?.focus()
    if (cmd === 'createLink') { const url = prompt('Link (https://...)'); if (url) document.execCommand(cmd, false, url) }
    else document.execCommand(cmd)
  })
  metaForm.addEventListener('submit', async e => {
    e.preventDefault()
    const f = new FormData(metaForm)
    // publishing has its own button (Status panel); the form never touches it
    const body: Record<string, unknown> = { id: metaForm.dataset.id, name: f.get('name') }
    for (const k of ['version', 'author', 'publishingYear', 'printedYear']) if (f.has(k)) body[k] = f.get(k)
    if (desc) body.description = desc.innerHTML
    const status = $('#save-status')!
    status.textContent = 'Saving...'
    try {
      const saved = await api('save', body)
      status.textContent = `Saved ${new Date().toLocaleTimeString()}`
      if (desc) desc.innerHTML = saved.description || ''
    } catch (err) { status.textContent = ''; toast((err as Error).message, true) }
  })
}

// ---- pdf.js (loaded only when needed) ------------------------------------------------------
type PdfDoc = { numPages: number; getPage(n: number): Promise<any> }
async function pdfjs() {
  const lib = await import('pdfjs-dist')
  const worker = (await import('pdfjs-dist/build/pdf.worker.min.mjs?url')).default
  lib.GlobalWorkerOptions.workerSrc = worker
  return lib
}
async function openPdf(src: string | ArrayBuffer): Promise<PdfDoc> {
  const lib = await pdfjs()
  // decoders/fonts copied there by scripts/build-assets.mjs; absolute, since the worker resolves relative URLs against itself
  const base = `${location.origin}/fs-static/pdfjs-${lib.version}`
  return lib.getDocument({
    ...(typeof src === 'string' ? { url: src } : { data: new Uint8Array(src) }),
    wasmUrl: `${base}/wasm/`, cMapUrl: `${base}/cmaps/`, standardFontDataUrl: `${base}/standard_fonts/`, iccUrl: `${base}/iccs/`,
  }).promise
}
/** Render a page at 1500 px height (as the old thumbnail generator did) on a white background. */
async function renderPage(doc: PdfDoc, pageNum: number, height = 1500) {
  const page = await doc.getPage(pageNum)
  const vp1 = page.getViewport({ scale: 1 })
  const viewport = page.getViewport({ scale: height / vp1.height })
  const canvas = document.createElement('canvas')
  canvas.width = Math.round(viewport.width); canvas.height = Math.round(viewport.height)
  await page.render({ canvas, viewport, background: 'white' }).promise
  return canvas
}
const toJpeg = (c: HTMLCanvasElement) => new Promise<Blob>((ok, bad) => c.toBlob(b => (b ? ok(b) : bad(new Error('jpeg failed'))), 'image/jpeg', 0.8))

async function uploadPreviews(id: string | number, canvases: HTMLCanvasElement[], pages: number[], numPages: number) {
  const { urls } = await api<{ urls: string[] }>('preview-urls', { id, count: canvases.length })
  for (let i = 0; i < canvases.length; i++) {
    const res = await fetch(urls[i], { method: 'PUT', body: await toJpeg(canvases[i]), headers: { 'Content-Type': 'image/jpeg', 'x-amz-acl': 'public-read' } })
    if (!res.ok) throw new Error(`preview upload failed (${res.status})`)
  }
  await api('previews', { id, pages, numPages })
}
const defaultPages = (n: number) => (n > 1 ? [1, Math.floor(n / 2) + 1] : [1])

const previewBox = $('#previews')
if (previewBox) {
  const id = previewBox.dataset.id!
  const status = $('#pdf-status')!, out = $('#preview-canvases')!
  const pagesInput = $<HTMLInputElement>('input[name=pages]', previewBox)!
  const saveBtn = $<HTMLButtonElement>('[data-save-previews]', previewBox)!
  let doc: PdfDoc | undefined, rendered: { canvases: HTMLCanvasElement[]; pages: number[] } | undefined
  const load = async () => {
    if (doc) return doc
    status.textContent = 'Loading PDF...'
    const { url } = await api<{ url: string }>('source-url', { id })
    doc = await openPdf(url)
    status.textContent = `${doc.numPages} pages`
    if (!pagesInput.value.trim()) pagesInput.value = defaultPages(doc.numPages).join(', ')
    return doc
  }
  const fail = (err: unknown) => { status.textContent = ''; toast(`${(err as Error).message} - is bucket CORS set?`, true) }
  $('[data-render]', previewBox)!.addEventListener('click', async () => {
    try {
      const d = await load()
      const pages = pagesInput.value.split(/[,\s]+/).map(Number).filter(n => n >= 1 && n <= d.numPages).slice(0, 6)
      if (!pages.length) return toast('Enter page numbers', true)
      status.textContent = 'Rendering...'
      out.replaceChildren()
      const canvases = []
      for (const p of pages) {
        const c = await renderPage(d, p)
        c.className = 'w-full h-auto rounded-md border border-border cursor-zoom-in'
        c.dataset.zoom = ''
        c.dataset.caption = `Page ${p} (not uploaded yet)`
        out.append(c); canvases.push(c)
      }
      rendered = { canvases, pages }
      saveBtn.disabled = false
      status.textContent = `Rendered pages ${pages.join(', ')}`
    } catch (err) { fail(err) }
  })
  saveBtn.addEventListener('click', async () => {
    if (!rendered || !doc) return
    saveBtn.disabled = true
    status.textContent = 'Uploading...'
    try { await uploadPreviews(id, rendered.canvases, rendered.pages, doc.numPages); status.textContent = 'Previews saved'; toast('Previews saved') }
    catch (err) { saveBtn.disabled = false; fail(err) }
  })
}

// ---- upload page -------------------------------------------------------------------------------
const input = $<HTMLInputElement>('#upload-input')
if (input) {
  const list = $('#upload-list')!, start = $<HTMLButtonElement>('#upload-start')!, drop = $('#drop')!
  const folderSel = $<HTMLSelectElement>('#upload-folder')!
  type Item = { file: File; row: HTMLElement; done?: boolean }
  let items: Item[] = []
  const parse = (fileName: string) => {
    const m = /^(.+?)(?:\[(.*)\])?(?:\{\d+\})?(?:\.(\w+))?$/.exec(fileName)
    return { name: m?.[1].trim() || fileName, version: m?.[2]?.trim() || '' }
  }
  const esc = (s: string) => s.replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]!))
  function add(files: FileList | File[]) {
    for (const file of files) {
      const { name, version } = parse(file.name)
      const row = document.createElement('div')
      row.className = 'border border-border rounded-lg p-4 grid grid-cols-1 sm:grid-cols-6 gap-3 bg-background/40'
      row.innerHTML = `<div class="sm:col-span-6 flex items-center justify-between gap-3 text-[13px]"><span class="font-medium break-all">${esc(file.name)}</span><span class="text-text-muted tabular-nums shrink-0">${(file.size / 1048576).toFixed(1)} MB</span></div>
        <div class="sm:col-span-4"><label class="label">Name</label><input class="field" data-k="name" value="${esc(name)}" /></div>
        <div class="sm:col-span-2"><label class="label">Version / note</label><input class="field" data-k="version" value="${esc(version)}" /></div>
        <div class="sm:col-span-2"><label class="label">Author</label><input class="field" data-k="author" /></div>
        <div class="sm:col-span-2"><label class="label">Publishing year</label><input class="field" data-k="publishingYear" inputmode="numeric" /></div>
        <div class="sm:col-span-2"><label class="label">Printed year</label><input class="field" data-k="printedYear" inputmode="numeric" /></div>
        <div class="sm:col-span-6 h-1.5 bg-surface-hover rounded-full overflow-hidden"><div data-bar class="h-full bg-primary w-0 transition-all"></div></div>
        <div data-msg class="sm:col-span-6 text-[13px] text-text-muted empty:hidden"></div>`
      list.append(row)
      items.push({ file, row })
    }
    start.disabled = !items.some(i => !i.done)
    $('#upload-summary')!.textContent = `${items.filter(i => !i.done).length} file(s) ready`
  }
  input.addEventListener('change', () => input.files && add(input.files))
  drop.addEventListener('dragover', e => { e.preventDefault(); drop.classList.add('border-primary') })
  drop.addEventListener('dragleave', () => drop.classList.remove('border-primary'))
  drop.addEventListener('drop', e => { e.preventDefault(); drop.classList.remove('border-primary'); if (e.dataTransfer?.files) add(e.dataTransfer.files) })

  const put = (url: string, file: File, contentType: string, onProgress: (f: number) => void) => new Promise<void>((ok, bad) => {
    const xhr = new XMLHttpRequest()
    xhr.open('PUT', url)
    xhr.setRequestHeader('Content-Type', contentType)
    xhr.upload.onprogress = e => e.lengthComputable && onProgress(e.loaded / e.total)
    xhr.onload = () => (xhr.status < 300 ? ok() : bad(new Error(`upload failed (${xhr.status})`)))
    xhr.onerror = () => bad(new Error('upload failed - is bucket CORS set?'))
    xhr.send(file)
  })

  start.addEventListener('click', async () => {
    start.disabled = true
    for (const it of items.filter(i => !i.done)) {
      const v = (k: string) => it.row.querySelector<HTMLInputElement>(`[data-k=${k}]`)!.value
      const bar = it.row.querySelector<HTMLElement>('[data-bar]')!, msg = it.row.querySelector<HTMLElement>('[data-msg]')!
      try {
        const parentId = Number(folderSel.value)
        const up = await api('upload-url', { parentId, fileName: it.file.name, name: v('name'), version: v('version'), contentType: it.file.type || undefined })
        await put(up.url, it.file, up.contentType, f => (bar.style.width = `${Math.round(f * 100)}%`))
        const e = await api('commit', { id: up.id, key: up.key, parentId, name: up.name, version: up.version, type: up.type, size: it.file.size,
          author: v('author'), publishingYear: v('publishingYear'), printedYear: v('printedYear') })
        it.done = true
        msg.innerHTML = `Uploaded as <a class="text-info" href="${base}/admin/edit/${e.id}">#${e.id}</a>.`
        if (up.type === 'pdf') {
          msg.append(' Rendering previews...')
          try {
            const doc = await openPdf(await it.file.arrayBuffer())
            const pages = defaultPages(doc.numPages)
            const canvases = []
            for (const p of pages) canvases.push(await renderPage(doc, p))
            await uploadPreviews(e.id, canvases, pages, doc.numPages)
            msg.append(` done (${doc.numPages} pages).`)
          } catch (err) { msg.append(` previews failed: ${(err as Error).message}`) }
        }
      } catch (err) {
        msg.textContent = (err as Error).message
        msg.classList.add('!text-rose-500')
      }
    }
    start.disabled = !items.some(i => !i.done)
  })
}

// all handlers attached (used by test/e2e to know the page is interactive)
document.body.dataset.ready = '1'

export {}
