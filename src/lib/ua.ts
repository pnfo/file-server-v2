// In-app browser (webview) detection for the "open in Chrome/Safari" helper (design §5.5).
const IN_APP = /FBAN|FBAV|FB_IAB|FBIOS|FB4A|Instagram|Messenger|MessengerForiOS|Line\/|MicroMessenger|Snapchat|TikTok|musical_ly|BytedanceWebview|Twitter for/i

export interface BrowserInfo {
  inApp: boolean
  appName: string | null
  os: 'android' | 'ios' | 'other'
}

export function detectBrowser(ua = ''): BrowserInfo {
  const os = /Android/i.test(ua) ? 'android' : /iPhone|iPad|iPod/i.test(ua) ? 'ios' : 'other'
  const m = IN_APP.exec(ua)
  let appName: string | null = null
  if (m) {
    const t = m[0].toLowerCase()
    appName = t.startsWith('insta') ? 'Instagram' : t.startsWith('messenger') ? 'Messenger'
      : t.startsWith('fb') ? 'Facebook' : t.startsWith('line') ? 'LINE' : t.includes('micromessenger') ? 'WeChat'
      : t.startsWith('snap') ? 'Snapchat' : t.startsWith('twitter') ? 'X' : 'TikTok'
  }
  return { inApp: !!m, appName, os }
}

/** Android intent that opens `url` (our file page) in Chrome, falling back to the same URL. */
export function chromeIntent(url: string) {
  const u = new URL(url)
  return `intent://${u.host}${u.pathname}${u.search}#Intent;scheme=${u.protocol.replace(':', '')};`
    + `package=com.android.chrome;S.browser_fallback_url=${encodeURIComponent(url)};end`
}
