// Usage: npm run sync -- <library|cloud> [--dry-run] [--counts path/to/id-to-info.json]
// Lists the bucket and upserts entries. On a first import pass --counts to seed download counts and dates.
import { SITE_IDS, type SiteId } from '../src/lib/config.ts'
import { SiteStore } from '../src/lib/store.ts'
import { readCountsFile, syncFromS3 } from '../src/lib/sync.ts'

const args = process.argv.slice(2)
const site = args.find(a => !a.startsWith('--') && SITE_IDS.includes(a as SiteId)) as SiteId | undefined
if (!site) { console.error(`usage: npm run sync -- <${SITE_IDS.join('|')}> [--dry-run] [--counts file]`); process.exit(1) }
const countsIdx = args.indexOf('--counts')
const store = new SiteStore(site)
const counts = countsIdx >= 0 ? readCountsFile(args[countsIdx + 1]) : undefined
const r = await syncFromS3(store, { dryRun: args.includes('--dry-run'), counts })
console.log(`[${site}] ${r.dryRun ? 'DRY RUN - ' : ''}objects ${r.objects}, files ${r.files}, folders ${r.folders}`)
console.log(`added ${r.added.length}, updated ${r.updated.length}, restored ${r.restored.length}, missing ${r.missing.length}, skipped ${r.skipped.length}`)
for (const s of r.skipped.slice(0, 50)) console.log(`  skipped: ${s.key} - ${s.reason}`)
if (r.missing.length) console.log(`  missing ids: ${r.missing.slice(0, 50).join(', ')}`)
