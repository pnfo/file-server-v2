import assert from 'node:assert/strict'
import { test } from 'node:test'
import os from 'node:os'
import path from 'node:path'
import fs from 'node:fs'
import { SiteDb, type Entry } from '../src/lib/db.ts'
import { SiteStore } from '../src/lib/store.ts'

const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'fs-test-'))
const mk = (p: Partial<Entry> & Pick<Entry, 'id' | 'name'>): Entry => ({
  parentId: 0, kind: 'file', type: 'pdf', version: null, author: null, publishingYear: null, printedYear: null, description: null,
  s3Key: `${p.name}{${p.id}}.pdf`, sizeBytes: 100, numEntries: 0, numPages: null, previewPages: null, downloads: 0, isPublished: true,
  dateAdded: '2026-01-01', updatedAt: '', missingSince: null, ...p,
})
const db = new SiteDb('library', path.join(tmp, 't.db'))
db.saveMany([
  mk({ id: 1, name: 'අභිධර්ම', kind: 'folder', type: 'folder' }),
  mk({ id: 2, name: 'හිමි පොත', parentId: 1, downloads: 5 }),
  mk({ id: 3, name: 'English book', parentId: 1 }),
  mk({ id: 4, name: 'hidden folder', kind: 'folder', type: 'folder', isPublished: false }),
  mk({ id: 5, name: 'inside hidden', parentId: 4 }),
  mk({ id: 6, name: 'gone', parentId: 1, missingSince: '2026-02-01' }),
])
const store = new SiteStore('library', db)

test('folder totals and visibility', () => {
  assert.equal(store.get(1)!.numEntries, 2)
  assert.equal(store.get(1)!.sizeBytes, 200)
  assert.equal(store.get(4), undefined)
  assert.equal(store.get(5), undefined, 'unpublished folder hides its subtree')
  assert.equal(store.get(6), undefined, 'missing files are hidden')
  assert.deepEqual(store.childrenOf(0).map(e => e.id), [1])
  assert.deepEqual(store.parents(2).map(e => e.id), [1])
})

test('singlish search matches sinhala names and english case-insensitively', () => {
  assert.deepEqual(store.search('himi').entries.map(e => e.id), [2])
  assert.deepEqual(store.search('ENGLISH').entries.map(e => e.id), [3])
  assert.deepEqual(store.search('inside').entries, [])
})

test('hits are counted once per client per 10 minutes', () => {
  assert.equal(store.hit(2, 'a'), true)
  assert.equal(store.hit(2, 'a'), false)
  assert.equal(store.hit(2, 'b'), true)
  assert.equal(db.get(2)!.downloads, 7)
  assert.equal(store.hit(1, 'a'), false, 'folders are not counted')
})
