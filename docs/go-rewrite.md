# File Server v2: Go rewrite

**Status:** Live since 2026-10-05; the Node server, PM2 and the Astro code are removed. This document records the port: what was kept, how it maps to the old code, and how it was checked.
**Replaces:** the Astro/Node server in this repo (PM2 app `file-server-v2`; the code is in git history before this port). Same behaviour, URLs, HTML, data and bucket.
**Read with:** [design.md](design.md), which still holds the reasoning behind every behaviour kept here (SigV2 links, counting, in-app browsers, admin flows), and [design-concerns.md](design-concerns.md) for the UI parity rules.

---

## 1. Why

The droplet (`new-tipitaka.lk-1gb-sgp1`, 1 GB RAM) is getting more services. Measured on 2026-10-05:

| Process | RSS |
|---|---|
| `node dist/server/entry.mjs` (file-server-v2) | 122 MB |
| PM2 God daemon | 65 MB |
| **Go file server (test instance, both sites loaded)** | **28 MB** |
| `tipitaka_lk_linux_intel` (Go) | 46 MB |
| `tipitaka_lk_new_linux_intel` (Go) | 23 MB |

The switch frees about **160 MB**, and all three services are then deployed and run the same way (a Go binary in `~/www/<name>` run by a systemd unit).

## 2. Decisions

| # | Decision |
|---|---|
| G1 | **Behaviour, URLs, HTML and data stay as they are.** A port, not a redesign. Every rule in design.md §5–8 carries over. Checked by the parity crawl (§9). |
| G2 | **One static Go binary** (`CGO_ENABLED=0`, cross-compiled on the Mac, `server/`, `package main` like tipitaka.lk's `server/`) serves `/library` and `/cloud`. It runs under **systemd** (`server/file_server.service`). No PM2, no Node and no `npm ci` on the droplet. |
| G3 | **Same SQLite files** (same schema and `PRAGMA user_version`), opened with `modernc.org/sqlite` (pure Go). No data migration. Either server can run on the files the other wrote. |
| G4 | **Search stays server-side**, using the Go port of `@pnfo/singlish-search` (`github.com/pnfo/singlish-search`, §5). |
| G5 | **Pages use `html/template`**, with the same markup and classes as the `.astro` components, and template whitespace removed the way Astro's `compressHTML` does. |
| G6 | **Front-end assets are built on the Mac** by `scripts/build-assets.mjs`: esbuild for `src/client/*.ts` and the Tailwind CLI for CSS, both pinned in package-lock. It also copies the pdf.js assets and writes `manifest.json`. Node stays a dev-machine tool. |
| G7 | **`sync`, `backup` and `snapshot` are subcommands of the binary**, replacing `scripts/sync.ts` and `scripts/backup.ts` on the server. `scripts/page-counts.ts` stays a Node script run on the Mac. |
| G8 | **Content freeze until cutover:** no new files are added while both servers exist, so the test instance's database snapshot stays representative and no schema change has to be made twice. |

## 3. What does not change

- Public URLs: `/{site}/`, `/{site}/{id}`, `/{site}/{id}/download`, `/{site}/{id}/all`, `/{site}/search/{q}`, `/{site}/search?q=`, `/{site}/recent/{months}`, `/{site}/api/{hit,link,search,health}`, `/{site}/admin/*`.
- SigV2 link format, 6-hour CDN-friendly expiry windows, `response-content-disposition`, `DROPLET_SHARE` / `DROPLET_BASE`.
- Download counting: `sendBeacon` hit, server-side count on `/download`, 10-minute repeat filter per (client, file).
- Bucket naming (`name[version]{id}.type`), S3 as the source of truth for which files exist, `missing_since`, unpublished subtrees.
- `config/library.json`, `config/cloud.json`, `config/secrets.json`; env vars `PORT`, `HOST`, `SITE_ALIASES`, `LEGACY_DOWNLOAD_MODE`, `LOG_REQUESTS`, `DROPLET_SHARE`, `DROPLET_BASE`, `APP_ROOT`.
- Browser code in `src/client/` and the admin's pdf.js previews.
- The `fs_admin` cookie format, so admin sessions survive the cutover.

Small known differences: the admin pages load one stylesheet instead of Astro's two; `/api/health` adds `"server": "go"`; `text/html` responses say `charset=utf-8`.

## 4. Layout

| Now (`src/`) | Go (`server/`) |
|---|---|
| `lib/config.ts`, `lib/site.ts` | `config.go` |
| `lib/db.ts` | `db.go` (`modernc.org/sqlite`, WAL, one connection; backup with `VACUUM INTO`) |
| `lib/store.ts` | `store.go`: in-memory index, visibility, folder totals, search, recent, hit filter. An `RWMutex` guards it (Go handlers run concurrently, Node's didn't); `writeMu` serialises admin writes and syncs. |
| `lib/sign.ts` | `sign.go` |
| `lib/naming.ts`, `lib/format.ts`, `lib/ua.ts`, `lib/sanitize.ts` | `naming.go`, `format.go`, `ua.go`, `sanitize.go`; `jsutil.go` reproduces the JS behaviour they relied on (`Number()`, truthiness, `parseInt`, `toFixed`, `encodeURIComponent`, UTF-16 `slice`) |
| `lib/auth.ts` | `auth.go` |
| `lib/s3.ts` (`@aws-sdk/*`) | `s3.go` (`minio-go`) |
| `lib/sync.ts`, `lib/admin.ts`, `admin/api/[action].ts` | `sync.go`, `admin.go` |
| `lib/selfcheck.ts`, `api/health.ts` | `selfcheck.go` |
| `middleware.ts`, `pages/**` | `web.go` (routing, middleware, handlers, view models) |
| `layouts/*.astro`, `components/*.astro` | `templates/*.html` (`go:embed`), `render.go`; `icons.go` is generated by `scripts/gen-icons.mjs` from `@mdi/js` |
| `@pnfo/singlish-search` | `singlish.go` → `github.com/pnfo/singlish-search` (§5) |
| `Intl.Collator('si')` | `collate.go` (§6) |
| `public/{library,cloud}/*` | copied to `dist/{library,cloud}/`, served under the site prefix as before |

## 5. Search

`singlish.go` calls `github.com/pnfo/singlish-search` (v1.4.1, the Go port in the same repo as the JS library). It returns at most 300 matches, ranked most likely first. The Node server uses 1.2.5 (unranked, and a "too many variants" notice above 300), so search results differ between the two by design. That's why the parity crawl skipped search after the switch to 1.4.1. On the 279 distinct queries the parity crawl used, the Go library returns exactly what the JS v1.4.1 returns.

Matching is the same as `SiteStore.search`: lowercase the query, get the terms, then walk the visible entries in name order and keep those whose name, version or author contains the query or a term (50 results public, 500 in the admin). The 300-term check stays but can no longer trigger with 1.4.1.

## 6. Sinhala sort order

Listings sort folders first, then by `Intl.Collator('si')`, then by id. `x/text/collate` can't reproduce that directly:
- it has no CLDR script reordering (ICU's `si` sorts Sinhala before Latin);
- its Sinhala tailoring gives anusvara the same weight as ක (`"කං" == "කක"`), so such names would tie and fall back to id order.

`collate.go` therefore uses the root collation on rewritten text: Latin letters (after NFD) become Deseret, which sorts after Sinhala and keeps lower-before-upper; ං and ඃ become ඖ plus a Han ideograph, so they land after the vowels and before ක; ඤ and ඥ swap. `testdata/collation_si.txt` holds 5,147 strings sorted by ICU, with ties marked: every name in both databases plus 3,000 generated strings. Every adjacent pair compares the same in Go. Not handled: candrabindu ඁ, which isn't used in the data.

## 7. Front-end build

`node scripts/build-assets.mjs` (`npm run build` also builds the binary) writes `server/dist/`:
- `fs-assets/`: `site.<hash>.js`, `admin.<hash>.js` and their chunks, `site.<hash>.css`, `admin.<hash>.css`, and the pdf.js worker (emitted as `.js` for nginx's MIME table). `admin.ts`'s Vite-style `?url` import is handled by a small esbuild plugin, so the browser code is unchanged.
- `fs-static/`: favicon, fonts, `pdfjs-<version>/{wasm,cmaps,standard_fonts,iccs}`.
- `library/`, `cloud/`: per-site public files.
- `manifest.json`: logical name → hashed URL, read by the server at startup.

Tailwind scans the repo, so classes in `server/templates`, `server/*.go` and `src/client` are included. The CSS contains every selector of the Astro build.

## 8. Deployment

Same mechanism as the tipitaka.lk Go servers: `./deploy.sh` builds a linux binary and uploads it with `dist/`, the site config and the nginx files to `~/www/file_server` (`/var/www/html/file_server`). It then restarts the systemd unit `file_server` ([server/file_server.service](../server/file_server.service)) with sudo and checks `/api/health`. The previous binary stays as `file_server.prev`. The unit sets `GOMEMLIMIT=60MiB` (the GC works harder near it) and `MemoryMax=150M`. The nightly cron runs `./file_server backup`. See the README for details.

During the transition, a test instance ran side by side at `/library-v2` and `/cloud-v2` (port 8091, `SITE_ALIASES`), on a snapshot of the live databases.

## 9. Testing

The parity crawler (`server/tools/parity`, `scripts/parity.sh`) and the fixture generator (`scripts/gen-go-fixtures.ts`) needed the Node server and were removed after the cutover; they are in git history. The fixtures they produced stay in `server/testdata/`.


| Test | Result (2026-10-05) |
|---|---|
| `npm test`: Go unit tests, including vectors produced by the TypeScript code (`scripts/gen-go-fixtures.ts` → `server/testdata/`): size format, sanitizer, name parsing, link signing, UA detection, collation | pass |
| Parity crawl (`scripts/parity.sh`): Node and Go on the same database snapshot. Covers every entry page on both sites, plus in-app browser variants, recent pages, redirects, `/download`, the link API, 404s, and every admin edit/folder page. Before the Singlish library landed it also covered search for 296 queries, with Singlish terms supplied from the JS library: 26,837 requests, no differences. | **25,053 requests, no differences** |
| Production Node vs the Go test instance on the droplet (read-only: no `/download`, search or admin) | differences only in live download counts and in which host (CDN or `/fs-dl/`) the 40% droplet share picked |
| Admin writes against a database copy: login/logout, folder create/rename/delete, publish, metadata, validation errors, CSRF | same responses as Node |
| `npm run test:e2e` against the Go server. Uses the real bucket: uploads, pdf.js previews over CORS, renames, publish, folders, deletes. | **all 41 checks pass**, nothing left in the bucket |
| `file_server backup` with a throwaway bucket prefix | valid snapshots uploaded (then removed) |

## 10. Cutover (2026-10-05)

1. Test instance at `https://tipitaka.lk/library-v2/`: pages, downloads (also with `fbclid`), search, admin.
2. `pm2 stop file-server-v2`, then copy the final databases into `~/www/file_server/data` with `file_server snapshot`.
3. Install and start the `file_server` unit, then point the nginx includes at `~/www/file_server/nginx/`.
4. Switch the backup cron to `./file_server backup`.
5. Cleanup:
   - on the droplet: removed the PM2 app and daemon (the `pm2-janaka` unit was disabled), the test instance and its nginx include, and `~/file-server-v2` (its local backups were moved to `~/www/file_server/data/backups/`);
   - in the repo: removed the Astro code, the Node scripts and the transition tools.

Measured after the switch: the file server uses 35 MB instead of 122 MB for Node plus 65 MB for PM2.
