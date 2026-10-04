// File type presentation, ported from the production stores/utils.js (icons/labels) and
// design-concerns.md concern 3 (colours). Icons are keys into components/Icons.astro.

export interface TypeInfo {
  icon: string
  label: string
  downloadText: string
  viewText: string     // primary button for types the browser opens inline
  mime: string
  color: string // tailwind text colour class
}

const T = (icon: string, label: string, downloadText: string, mime: string, color: string, viewText = downloadText): TypeInfo =>
  ({ icon, label, downloadText, viewText, mime, color })

const typeToInfo: Record<string, TypeInfo> = {
  pdf: T('pdf', 'PDF', 'PDF ගොනුව බාගත කරගන්න', 'application/pdf', 'text-rose-400', 'PDF ගොනුව බලන්න'),
  htm: T('html', 'WEB', 'HTML ගොනුව බාගත කරගන්න', 'text/html', 'text-blue-400', 'HTML ගොනුව බලන්න'),
  col: T('folder', 'ගොනුව', 'බහාලුම අරින්න', '', 'text-amber-500'),
  zip: T('zip', 'ZIP', 'ZIP ගොනුව බාගත කරගන්න', 'application/zip', 'text-purple-400'),
  apk: T('android', 'APP', 'ඇන්ඩ්‍රොයිඩ් මෘදුකාංගය ලබාගන්න', 'application/vnd.android.package-archive', 'text-emerald-400'),
  doc: T('word', 'DOC', 'Word ගොනුව බාගත කරගන්න', 'application/msword', 'text-sky-400'),
  xls: T('excel', 'EXCEL', 'Excel වගුව බාගත කරගන්න', 'application/vnd.ms-excel', 'text-emerald-500'),
  jpg: T('image', 'IMAGE', 'පින්තූරය බාගත කරගන්න', 'image/jpeg', 'text-amber-400', 'පින්තූරය බලන්න'),
  png: T('image', 'IMAGE', 'පින්තූරය බාගත කරගන්න', 'image/png', 'text-amber-400', 'පින්තූරය බලන්න'),
  txt: T('text', 'TXT', 'TEXT ගොනුව බාගත කරගන්න', 'text/plain', 'text-amber-400', 'TEXT ගොනුව බලන්න'),
  mp3: T('audio', 'MP3', 'ධර්ම දේශනාව බාගත කරගන්න', 'audio/mpeg', 'text-emerald-400'),
  m4a: T('audio', 'M4A', 'ධර්ම දේශනාව බාගත කරගන්න', 'audio/mp4', 'text-purple-400'),
  unk: T('file', 'FILE', 'බාගත කරගන්න', 'application/octet-stream', 'text-amber-400'),
}

export function getTypeInfo(type = ''): TypeInfo {
  const raw = type.toLowerCase()
  if (raw === 'folder' || raw === 'coll') return typeToInfo.col
  const type3 = raw.startsWith('jpe') ? 'jpg' : raw.slice(0, 3)
  return typeToInfo[type3] || typeToInfo.unk
}

export const isAudio = (type: string) => type === 'mp3' || type === 'm4a'
/** Types a browser shows inline - these get a separate "save to device" link. */
export const isViewable = (type: string) => /^(pdf|html?|txt|jpe?g|png)$/.test(type)

const sizeUnits = ['Bytes', 'KB', 'MB', 'GB', 'TB']
export function getSizeInfo(size = 0): { numeral: string; unit: string } {
  if (!size || size <= 0) return { numeral: '0', unit: 'KB' }
  const i = Math.min(Math.floor(Math.log(size) / Math.log(1024)), sizeUnits.length - 1)
  const n = size / Math.pow(1024, i)
  return { numeral: n < 10 ? n.toFixed(1) : String(Math.round(n)), unit: sizeUnits[i] }
}
/** design-concerns.md rule 2: GB/TB red, everything else green. */
export const sizeUnitColor = (unit: string) =>
  unit === 'GB' || unit === 'TB' ? 'text-rose-500 dark:text-rose-400' : 'text-emerald-600 dark:text-emerald-400'

export const formatNumber = (n = 0) => String(n).replace(/(\d)(?=(\d{3})+(?!\d))/g, '$1,')

// UN-Abhaya is used for Sinhala only, in normal weight (no faux bold). The page font stack applies it to
// Sinhala characters automatically; a line that mixes Sinhala with Latin letters or digits gets the `si`
// class so the whole line is set in UN-Abhaya (e.g. "PDF ගොනුව බලන්න").
const SINHALA = /[\u0D80-\u0DFF]/, LATIN_OR_DIGIT = /[A-Za-z0-9]/
export const fontClass = (text: string | null | undefined) => (text && SINHALA.test(text) && LATIN_OR_DIGIT.test(text) ? 'si' : '')
