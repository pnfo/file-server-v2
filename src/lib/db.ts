import fs from 'node:fs'
import path from 'node:path'
// Node's built-in SQLite: no native module to install, so nothing depends on the server's glibc
// (better-sqlite3's prebuilt binary needs glibc 2.34; the droplet's Ubuntu 20.04 has 2.31).
import { DatabaseSync, backup, type StatementSync } from 'node:sqlite'
import { APP_ROOT, type SiteId } from './config.ts'

export interface Entry {
  id: number
  parentId: number          // 0 = root, -1 = unlisted (inside a folder segment without an {id})
  kind: 'folder' | 'file'
  type: string              // pdf, mp3, ... ('folder' for folders)
  name: string
  version: string | null    // the [version] tag in the file name
  author: string | null
  publishingYear: number | null
  printedYear: number | null
  description: string | null
  s3Key: string             // relative to the site root folder in the bucket
  sizeBytes: number         // folders: sum of published descendant files (computed in the store)
  numEntries: number        // folders: count of published descendant files (computed in the store)
  numPages: number | null
  previewPages: number[] | null // null = legacy thumbs/{id}-{0,1}.jpg
  downloads: number
  isPublished: boolean
  dateAdded: string         // YYYY-MM-DD
  updatedAt: string
  missingSince: string | null
}

// Each item runs once, in order, tracked by PRAGMA user_version.
const MIGRATIONS = [
  `CREATE TABLE entries (
    id              INTEGER PRIMARY KEY,
    parent_id       INTEGER NOT NULL DEFAULT 0,
    kind            TEXT NOT NULL CHECK (kind IN ('folder', 'file')),
    type            TEXT NOT NULL,
    name            TEXT NOT NULL,
    version         TEXT,
    author          TEXT,
    publishing_year INTEGER,
    printed_year    INTEGER,
    description     TEXT,
    s3_key          TEXT NOT NULL,
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    num_pages       INTEGER,
    preview_pages   TEXT,
    downloads       INTEGER NOT NULL DEFAULT 0,
    is_published    INTEGER NOT NULL DEFAULT 1,
    date_added      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    missing_since   TEXT
  );
  CREATE INDEX entries_parent ON entries(parent_id);
  CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT);`,
]

export const today = () => new Date().toISOString().slice(0, 10)

type Row = Record<string, any>
const fromRow = (r: Row): Entry => ({
  id: r.id, parentId: r.parent_id, kind: r.kind, type: r.type, name: r.name, version: r.version,
  author: r.author, publishingYear: r.publishing_year, printedYear: r.printed_year, description: r.description,
  s3Key: r.s3_key, sizeBytes: r.size_bytes, numEntries: 0, numPages: r.num_pages,
  previewPages: r.preview_pages ? JSON.parse(r.preview_pages) : null,
  downloads: r.downloads, isPublished: !!r.is_published, dateAdded: r.date_added, updatedAt: r.updated_at,
  missingSince: r.missing_since,
})
const toRow = (e: Entry) => ({
  id: e.id, parent_id: e.parentId, kind: e.kind, type: e.type, name: e.name, version: e.version,
  author: e.author, publishing_year: e.publishingYear, printed_year: e.printedYear, description: e.description,
  s3_key: e.s3Key, size_bytes: e.kind === 'file' ? e.sizeBytes : 0, num_pages: e.numPages,
  preview_pages: e.previewPages ? JSON.stringify(e.previewPages) : null,
  downloads: e.downloads, is_published: e.isPublished ? 1 : 0, date_added: e.dateAdded, updated_at: e.updatedAt,
  missing_since: e.missingSince,
})

export class SiteDb {
  readonly db: DatabaseSync
  private stmts: Record<'all' | 'get' | 'upsert' | 'delete' | 'hit' | 'getMeta' | 'setMeta', StatementSync>

  constructor(site: SiteId, file = path.join(APP_ROOT, 'data', `${site}.db`)) {
    fs.mkdirSync(path.dirname(file), { recursive: true })
    this.db = new DatabaseSync(file)
    this.db.exec('PRAGMA journal_mode = WAL; PRAGMA synchronous = NORMAL; PRAGMA busy_timeout = 5000;')
    const version = (this.db.prepare('PRAGMA user_version').get() as Row).user_version as number
    for (let v = version; v < MIGRATIONS.length; v++) {
      this.transaction(() => { this.db.exec(MIGRATIONS[v]); this.db.exec(`PRAGMA user_version = ${v + 1}`) })
    }
    const cols = Object.keys(toRow({} as Entry))
    this.stmts = {
      all: this.db.prepare('SELECT * FROM entries'),
      get: this.db.prepare('SELECT * FROM entries WHERE id = ?'),
      upsert: this.db.prepare(`INSERT INTO entries (${cols.join(', ')}) VALUES (${cols.map(c => '@' + c).join(', ')})
        ON CONFLICT(id) DO UPDATE SET ${cols.filter(c => c !== 'id').map(c => `${c} = excluded.${c}`).join(', ')}`),
      delete: this.db.prepare('DELETE FROM entries WHERE id = ?'),
      hit: this.db.prepare('UPDATE entries SET downloads = downloads + 1 WHERE id = ?'),
      getMeta: this.db.prepare('SELECT v FROM meta WHERE k = ?'),
      setMeta: this.db.prepare('INSERT INTO meta (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v'),
    }
  }

  private transaction(fn: () => void) {
    this.db.exec('BEGIN')
    try { fn(); this.db.exec('COMMIT') } catch (err) { this.db.exec('ROLLBACK'); throw err }
  }

  all(): Entry[] { return this.stmts.all.all().map(r => fromRow(r as Row)) }
  get(id: number): Entry | undefined { const r = this.stmts.get.get(id); return r ? fromRow(r as Row) : undefined }
  save(e: Entry) { this.stmts.upsert.run(toRow(e) as Record<string, any>) }
  saveMany(list: Entry[]) { this.transaction(() => list.forEach(e => this.save(e))) }
  delete(id: number) { this.stmts.delete.run(id) }
  hit(id: number) { this.stmts.hit.run(id) }
  getMeta(k: string): string | undefined { return (this.stmts.getMeta.get(k) as Row | undefined)?.v }
  setMeta(k: string, v: string) { this.stmts.setMeta.run(k, v) }
  backup(dest: string) { return backup(this.db, dest) }
}
