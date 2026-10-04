// Allow-list for the description HTML written in the admin editor (rendered with set:html on public pages).
const ALLOWED = new Set(['p', 'br', 'b', 'strong', 'i', 'em', 'u', 'ul', 'ol', 'li', 'a', 'h3', 'h4', 'blockquote'])

export function sanitizeHtml(html: string | null | undefined): string | null {
  if (!html) return null
  const out = html
    .replace(/<!--[\s\S]*?-->/g, '')
    .replace(/<(script|style|iframe|object|embed|template|svg|math)[\s\S]*?<\/\1\s*>/gi, '')
    .replace(/<\/?([a-z0-9]+)([^>]*)>/gi, (tag, name: string, attrs: string) => {
      const n = name.toLowerCase()
      if (!ALLOWED.has(n)) return ''
      if (tag.startsWith('</')) return `</${n}>`
      if (n === 'a') {
        const href = /href\s*=\s*("([^"]*)"|'([^']*)')/i.exec(attrs)
        const url = (href?.[2] ?? href?.[3] ?? '').trim()
        return /^https?:\/\//i.test(url) ? `<a href="${url.replace(/"/g, '&quot;')}" rel="nofollow noopener">` : '<a>'
      }
      return `<${n}>`
    })
    .trim()
  return out.replace(/<[^>]*>/g, '').trim() ? out : null
}
