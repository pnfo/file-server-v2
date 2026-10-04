// All public-page behaviour, as progressive enhancement over server-rendered HTML.
// Every feature works on the existing DOM; nothing re-renders the page (design D6).

import { initLightbox } from './lightbox.ts'

const base = document.body.dataset.base || ''
initLightbox({ close: 'වසන්න', prev: 'පෙර පිටුව', next: 'ඊළඟ පිටුව' })
const $ = <T extends Element = HTMLElement>(sel: string, root: ParentNode = document) => root.querySelector<T>(sel)
const store = {
  get(k: string) { try { return localStorage.getItem(k) } catch { return null } },
  set(k: string, v: string) { try { localStorage.setItem(k, v) } catch {} },
}

// ---- toast + copy link --------------------------------------------------------------------
let toastTimer: number | undefined
function toast(msg: string) {
  const t = $('#toast')
  if (!t) return
  t.textContent = msg
  t.classList.remove('opacity-0')
  clearTimeout(toastTimer)
  toastTimer = window.setTimeout(() => t.classList.add('opacity-0'), 2500)
}
async function copy(text: string) {
  try { await navigator.clipboard.writeText(text) } catch {
    const ta = Object.assign(document.createElement('textarea'), { value: text })
    document.body.append(ta); ta.select(); document.execCommand('copy'); ta.remove()
  }
  toast('සබැඳිය පිටපත් විය. ඔබට අවශ්‍ය තැනක අලවන්න.')
}

// ---- header: theme + mobile menu --------------------------------------------------------
const menuBtn = $('#menu-btn'), menu = $('#menu')
function setMenu(open: boolean) { if (menu && menuBtn) { menu.hidden = !open; menuBtn.setAttribute('aria-expanded', String(open)) } }

// ---- file page: count downloads, refresh expired links ------------------------------------
function beacon(id: string) {
  const url = `${base}/api/hit/${id}`
  if (!navigator.sendBeacon?.(url)) fetch(url, { method: 'POST', keepalive: true }).catch(() => {})
}
const expiringSoon = (el: HTMLElement) => Number(el.dataset.expires || 0) * 1000 - Date.now() < 10 * 60_000
async function freshLink(el: HTMLElement) {
  const r = await fetch(`${base}/api/link/${el.dataset.hit}?mode=${el.dataset.mode || 'inline'}`)
  const { url, expires } = await r.json() as { url: string; expires: number }
  el.dataset.expires = String(expires)
  return url
}

document.addEventListener('click', async e => {
  const target = e.target as HTMLElement
  const copyBtn = target.closest<HTMLElement>('[data-copy]')
  if (copyBtn) { copy(copyBtn.dataset.copy!); return }
  if (target.closest('[data-theme-toggle]')) {
    const dark = document.documentElement.classList.toggle('dark')
    store.set('fs-theme', dark ? 'dark' : 'light')
    return
  }
  if (menuBtn?.contains(target)) { setMenu(!!menu?.hidden); return }
  if (menu && !menu.hidden && !menu.contains(target)) setMenu(false)

  const sortBtn = target.closest<HTMLElement>('[data-sort]')
  if (sortBtn) {
    const table = sortBtn.closest<HTMLElement>('[data-entry-table]')!
    const key = sortBtn.dataset.sort!
    const dir = table.dataset.sortKey === key && table.dataset.sortDir === 'asc' ? 'desc' : 'asc'
    sortTable(table, key, dir)
    store.set('fs-sort', JSON.stringify({ key, dir }))
    return
  }

  const link = target.closest<HTMLAnchorElement>('a[data-hit]')
  if (link) {
    beacon(link.dataset.hit!)
    // a page left open past the link window: fetch a fresh link and go - an in-page navigation, not an interstitial
    if (expiringSoon(link) && !e.defaultPrevented && e.button === 0 && !e.metaKey && !e.ctrlKey) {
      e.preventDefault()
      try { location.href = link.href = await freshLink(link) } catch { location.href = link.href }
    }
  }
})

const audio = $<HTMLAudioElement>('audio[data-hit]')
if (audio) {
  audio.addEventListener('play', async () => {
    beacon(audio.dataset.hit!)
    if (expiringSoon(audio)) {
      try { const t = audio.currentTime; audio.src = await freshLink(audio); audio.currentTime = t; audio.play() } catch {}
    }
  }, { once: true })
}

// ---- tables: client-side sort over the server-rendered rows -------------------------------
const collator = new Intl.Collator('si', { numeric: true })
const numeric = new Set(['size', 'downloads'])
const attr: Record<string, string> = { name: 'name', parent: 'parent', size: 'size', downloads: 'downloads', dateAdded: 'date' }

function sortTable(table: HTMLElement, key: string, dir: string) {
  const header = table.querySelector<HTMLElement>(`[data-sort="${key}"]`)
  const tbody = table.querySelector('tbody')
  if (!header || !tbody) return
  const a = attr[key], sign = dir === 'asc' ? 1 : -1
  const rows = [...tbody.rows]
  rows.sort((x, y) => {
    const fx = x.dataset.kind === 'folder', fy = y.dataset.kind === 'folder'
    if (fx !== fy) return fx ? -1 : 1 // folders always first
    const vx = x.dataset[a] || '', vy = y.dataset[a] || ''
    const c = numeric.has(key) ? Number(vx) - Number(vy) : collator.compare(vx, vy)
    return sign * c || collator.compare(x.dataset.name || '', y.dataset.name || '')
  })
  tbody.append(...rows)
  table.dataset.sortKey = key
  table.dataset.sortDir = dir
  table.querySelectorAll<HTMLElement>('[data-sort]').forEach(b => {
    const on = b.dataset.sort === key
    const [up, down] = b.querySelectorAll('svg')
    up?.classList.toggle('hidden', !(on && dir === 'asc'))
    down?.classList.toggle('hidden', !(on && dir === 'desc'))
    b.closest('th')?.setAttribute('aria-sort', on ? (dir === 'asc' ? 'ascending' : 'descending') : 'none')
  })
  const status = table.querySelector('[data-sort-status]')
  if (status) status.textContent = `ඉහත ලැයිස්තුව “${header.dataset.label}” ${dir === 'desc' ? 'අඩුවන' : 'වැඩිවන'} පිළිවෙළට සැකසී ඇත.`
}

/** Re-apply the sort the user last picked (also honours the old site's saved setting). */
function applySavedSort(root: ParentNode = document) {
  let saved: { key: string; dir: string } | undefined
  try {
    saved = JSON.parse(store.get('fs-sort') || 'null') || undefined
    if (!saved) {
      const old = JSON.parse(store.get('settings') || '{}').sortBy?.[0]
      if (old) saved = { key: old.key === 'Size' ? 'size' : old.key, dir: old.order }
    }
  } catch {}
  if (!saved) return
  root.querySelectorAll<HTMLElement>('[data-entry-table]').forEach(t => {
    if (t.querySelector(`[data-sort="${saved!.key}"]`) && (t.dataset.sortKey !== saved!.key || t.dataset.sortDir !== saved!.dir)) {
      sortTable(t, saved!.key, saved!.dir)
    }
  })
}
applySavedSort()

// ---- search: live results without losing focus -------------------------------------------
const input = $<HTMLInputElement>('#search-input')
const onSearchPage = () => location.pathname.startsWith(`${base}/search/`)
if (input) {
  let timer: number | undefined, seq = 0
  input.addEventListener('input', () => {
    clearTimeout(timer)
    const term = input.value.trim().toLowerCase()
    if (!term) return
    timer = window.setTimeout(async () => {
      const path = `${base}/search/${encodeURIComponent(term)}`
      const results = $('#search-results')
      if (!onSearchPage() || !results) {
        sessionStorage.setItem('fs-typing', '1') // so the results page takes focus back
        location.href = path
        return
      }
      const mine = ++seq
      try {
        const html = await (await fetch(`${base}/api/search?q=${encodeURIComponent(term)}`)).text()
        if (mine !== seq) return // a newer search already started
        const tpl = document.createElement('template')
        tpl.innerHTML = html
        const fresh = tpl.content.querySelector<HTMLElement>('#search-results')
        if (!fresh) throw new Error('bad fragment')
        results.replaceWith(fresh)
        applySavedSort(fresh)
        history.replaceState(null, '', path)
        if (fresh.dataset.title) document.title = fresh.dataset.title
      } catch { location.href = path }
    }, 400)
  })
  if (onSearchPage() && sessionStorage.getItem('fs-typing')) {
    sessionStorage.removeItem('fs-typing')
    input.focus()
    input.setSelectionRange(input.value.length, input.value.length)
  }
}

// ---- recent page: month selector ----------------------------------------------------------
const recentForm = $<HTMLFormElement>('[data-recent-form]')
recentForm?.querySelector('select')?.addEventListener('change', e => {
  location.href = `${base}/recent/${(e.target as HTMLSelectElement).value}`
})

export {}
