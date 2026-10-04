// Click an element with [data-zoom] to see it larger in a <dialog>: an <img> (or a wrapper around one),
// or a <canvas> (admin's freshly rendered previews). Elements inside the same [data-zoom-group] can be
// stepped through with the arrow buttons/keys. Esc, the close button or a click outside closes it.

export function initLightbox(labels: { close: string; prev: string; next: string }) {
  let dialog: HTMLDialogElement | undefined
  let items: HTMLElement[] = [], index = 0

  const srcOf = (el: HTMLElement) =>
    el instanceof HTMLCanvasElement ? el.toDataURL('image/jpeg', 0.92)
      : el instanceof HTMLImageElement ? el.currentSrc || el.src
      : el.querySelector('img')?.currentSrc || el.querySelector('img')?.src || ''

  function build() {
    const d = document.createElement('dialog')
    d.className = 'lightbox m-auto p-0 bg-transparent backdrop:bg-black/85 max-w-none max-h-none'
    const btn = 'absolute grid place-items-center size-11 rounded-full bg-black/60 text-white text-2xl hover:bg-black/80 cursor-pointer'
    d.innerHTML = `
      <figure class="relative flex flex-col items-center gap-2">
        <img data-lb-img alt="" class="block max-w-[96vw] max-h-[88vh] w-auto h-auto rounded-md bg-white shadow-2xl" />
        <figcaption data-lb-caption class="text-white/90 text-sm empty:hidden"></figcaption>
        <button type="button" data-lb-close class="${btn} top-2 right-2" aria-label="${labels.close}" title="${labels.close}">&times;</button>
        <button type="button" data-lb-prev class="${btn} left-2 top-1/2 -translate-y-1/2" aria-label="${labels.prev}" title="${labels.prev}">&#8249;</button>
        <button type="button" data-lb-next class="${btn} right-2 top-1/2 -translate-y-1/2" aria-label="${labels.next}" title="${labels.next}">&#8250;</button>
      </figure>`
    document.body.append(d)
    d.addEventListener('click', e => {
      const t = e.target as HTMLElement
      if (t === d || t.closest('[data-lb-close]')) d.close() // backdrop or close button
      else if (t.closest('[data-lb-prev]')) show(index - 1)
      else if (t.closest('[data-lb-next]')) show(index + 1)
    })
    d.addEventListener('keydown', e => {
      if (e.key === 'ArrowLeft') show(index - 1)
      if (e.key === 'ArrowRight') show(index + 1)
    })
    return d
  }

  function show(i: number) {
    if (!dialog || !items.length) return
    index = (i + items.length) % items.length
    const el = items[index]
    dialog.querySelector<HTMLImageElement>('[data-lb-img]')!.src = srcOf(el)
    dialog.querySelector('[data-lb-caption]')!.textContent = el.dataset.caption || ''
    for (const sel of ['[data-lb-prev]', '[data-lb-next]']) dialog.querySelector<HTMLElement>(sel)!.hidden = items.length < 2
  }

  document.addEventListener('click', e => {
    const el = (e.target as HTMLElement).closest<HTMLElement>('[data-zoom]')
    if (!el || el.closest('dialog')) return
    e.preventDefault()
    const group = el.closest('[data-zoom-group]')
    items = group ? [...group.querySelectorAll<HTMLElement>('[data-zoom]')] : [el]
    dialog ??= build()
    show(items.indexOf(el))
    dialog.showModal()
  })
}
