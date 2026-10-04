// Fill in num_pages for PDFs from local copies of the bucket (no download from Spaces).
//
//   node scripts/page-counts.ts library [--dir /Volumes/2TB/ebooks-new] [--fetch-missing] [--force] [--dry-run]
//
// The local folder mirrors the bucket: <dir>/<s3Key> (e.g. "අභිධර්ම{1}/x{2}.pdf"). Files whose local size differs
// from the bucket's are skipped (the local copy is a different version). --force recounts PDFs that already
// have a count. --fetch-missing reads the PDFs that aren't available locally straight from the bucket with HTTP
// range requests (pdf.js only fetches the parts it needs, usually a few KB per file).
// Writes data/<site>.db, so run it before the first deploy (or copy the DB afterwards).
import fs from 'node:fs'
import path from 'node:path'
import { getDocument } from 'pdfjs-dist/legacy/build/pdf.mjs'
import { SITE_IDS, type SiteId } from '../src/lib/config.ts'
import { getSiteConfig } from '../src/lib/config.ts'
import { SiteDb } from '../src/lib/db.ts'
import { presignGet } from '../src/lib/s3.ts'

const args = process.argv.slice(2)
const site = args.find(a => SITE_IDS.includes(a as SiteId)) as SiteId | undefined
const opt = (name: string) => { const i = args.indexOf(name); return i >= 0 ? args[i + 1] : undefined }
if (!site) { console.error('usage: node scripts/page-counts.ts <library|cloud> [--dir path] [--force] [--dry-run]'); process.exit(1) }
const dir = opt('--dir') || '/Volumes/2TB/ebooks-new'
const force = args.includes('--force'), dryRun = args.includes('--dry-run'), fetchMissing = args.includes('--fetch-missing')

/** Local path for a bucket key, trying both Unicode normal forms (macOS volumes may store either). */
function localFile(key: string) {
  for (const k of [key, key.normalize('NFC'), key.normalize('NFD')]) {
    const p = path.join(dir, k)
    if (fs.existsSync(p)) return p
  }
}

async function countPages(src: { file: string } | { url: string }) {
  const common = { disableFontFace: true, isEvalSupported: false, verbosity: 0 }
  const task = getDocument('file' in src
    ? { ...common, data: new Uint8Array(fs.readFileSync(src.file)) }
    : { ...common, url: src.url, disableAutoFetch: true, disableStream: true, rangeChunkSize: 65536 })
  try { return (await task.promise).numPages } finally { await task.destroy() }
}

const db = new SiteDb(site)
const pdfs = db.all().filter(e => e.type === 'pdf' && !e.missingSince && (force || !e.numPages))
const report = { counted: 0, fetched: 0, missing: [] as string[], sizeDiffers: [] as string[], failed: [] as string[] }
const root = getSiteConfig(site).s3RootFolder
const started = Date.now()
for (const [i, e] of pdfs.entries()) {
  const file = localFile(e.s3Key)
  const localOk = !!file && fs.statSync(file).size === e.sizeBytes
  if (!localOk && !fetchMissing) {
    if (!file) report.missing.push(`#${e.id} ${e.s3Key}`)
    else report.sizeDiffers.push(`#${e.id} ${e.s3Key} (local ${fs.statSync(file).size}, bucket ${e.sizeBytes})`)
    continue
  }
  try {
    const n = await countPages(localOk ? { file: file! } : { url: await presignGet(`${root}/${e.s3Key}`) })
    if (!localOk) report.fetched++
    if (!dryRun) db.save({ ...e, numPages: n }) // a computed fact, not an edit: updated_at stays
    report.counted++
    if (report.counted % 100 === 0) console.log(`  ${i + 1}/${pdfs.length} ... last #${e.id}: ${n} pages`)
  } catch (err) {
    report.failed.push(`#${e.id} ${e.s3Key}: ${(err as Error).message}`)
  }
}
console.log(`[${site}] ${dryRun ? 'DRY RUN - ' : ''}${pdfs.length} PDFs to count: ${report.counted} counted (${report.fetched} from the bucket), ${report.missing.length} not found locally, `
  + `${report.sizeDiffers.length} differ in size, ${report.failed.length} failed (${Math.round((Date.now() - started) / 1000)}s)`)
for (const [label, list] of [['not found locally', report.missing], ['size differs', report.sizeDiffers], ['failed', report.failed]] as const) {
  if (list.length) console.log(`${label}:\n  ${list.slice(0, 40).join('\n  ')}${list.length > 40 ? `\n  ... ${list.length - 40} more` : ''}`)
}
