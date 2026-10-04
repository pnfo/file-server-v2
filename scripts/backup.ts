// Usage: node scripts/backup.ts   (cron nightly) - snapshots each site DB and uploads it to <site>/backups/ (private).
import fs from 'node:fs'
import path from 'node:path'
import { APP_ROOT, SITE_IDS, getSiteConfig } from '../src/lib/config.ts'
import { SiteDb } from '../src/lib/db.ts'
import { putObject } from '../src/lib/s3.ts'

const stamp = new Date().toISOString().slice(0, 10)
for (const site of SITE_IDS) {
  const dir = path.join(APP_ROOT, 'data', 'backups')
  fs.mkdirSync(dir, { recursive: true })
  const file = path.join(dir, `${site}-${stamp}.db`)
  await new SiteDb(site).backup(file)
  await putObject(`${getSiteConfig(site).s3RootFolder}/backups/${site}-${stamp}.db`, fs.readFileSync(file), 'application/vnd.sqlite3')
  for (const old of fs.readdirSync(dir).filter(f => f.startsWith(site + '-')).sort().slice(0, -14)) fs.unlinkSync(path.join(dir, old))
  console.log(`backed up ${site} -> ${file}`)
}
