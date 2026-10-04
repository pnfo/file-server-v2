# File server v2

The download pages of [tipitaka.lk/library](https://tipitaka.lk/library/) and [tipitaka.lk/cloud](https://tipitaka.lk/cloud/), with an admin for the files in the `tipitaka` Spaces bucket.

Astro 7 (SSR, Node adapter) + Tailwind 4 + SQLite (Node's built-in `node:sqlite`, no native modules). One process serves `/library` and `/cloud`. Needs Node 22.18+; the droplet runs Node 24.

Design and reasoning: [docs/design.md](docs/design.md). UI parity rules and the download pitfalls it avoids: [docs/design-concerns.md](docs/design-concerns.md).

Downloads never pass through Node. File pages link straight to **SigV2-signed** Spaces CDN URLs. Unlike SigV4, these keep working when Facebook and other apps append `fbclid`. Links expire in 6–12 h, so the only lasting links to a file are our own pages, and those count every download.

## Layout

```
config/            library.json, cloud.json (site settings), secrets.json (git-ignored, see secrets.example.json)
data/              <site>.db SQLite files (git-ignored)
deploy/nginx/      nginx config, included from the droplet's site config
deploy.sh          build + upload + (re)start on the droplet
docs/              design notes
scripts/           sync.ts, backup.ts, page-counts.ts
src/lib/           server code: db, in-memory store + search, SigV2 signer, S3 access, sync, admin ops
src/pages/[site]/  routes; the first path segment picks the site
src/client/        browser JS: site.ts (public, ~4.5 KB), admin.ts (loads pdf.js on demand)
test/              unit tests (*.test.ts) and the browser end-to-end suite (e2e/)
```

## Commands

```bash
npm install
npm run dev                          # http://localhost:4321/library/
npm test && npx astro check          # unit tests, type check
npm run test:e2e                     # admin end-to-end in headless Chrome against the real bucket (see below)
./deploy.sh                          # deploy to the droplet (see below)

npm run sync -- library --dry-run    # bucket -> SQLite preview (also in admin -> Bucket)
npm run sync -- library              # keeps downloads and metadata
node scripts/backup.ts               # snapshot both DBs to <site>/backups/ in the bucket (nightly cron on the droplet)
node scripts/page-counts.ts library --fetch-missing   # fill in PDF page counts (local copies in /Volumes/2TB/ebooks-new, else range reads from the bucket)
```

## End-to-end test

`npm run test:e2e` drives the admin in headless Chrome against the dev server (started on :4321 if it isn't running) and the **real bucket**, in about a minute. It covers:
- creating, renaming, publishing and deleting folders;
- uploading a PDF and a text file;
- the name and key rules;
- browser-rendered and re-rendered previews;
- renaming files in the bucket;
- the public pages and signed links (with `fbclid`);
- deleting everything again, then checking nothing is left in the bucket.

It needs `config/secrets.json`, Google Chrome (or `CHROME=/path`), and the bucket CORS rule for `http://localhost:4321`. All its entries are named `fs-v2 e2e…` and are removed at the end, even after a failure. On a failure it saves a screenshot to `$TMPDIR/fs-e2e-failure.png`.

## Configuration

`config/secrets.json`:

| key | used for |
|---|---|
| `adminPassword` | `/<site>/admin/login` |
| `sessionSecret` | signs the admin session cookie (long random string) |
| `s3` | read/write Spaces key: sync, admin uploads/renames/deletes, backups |
| `signer` | **read-only** Spaces key for public download links. If it's missing, `s3` is used and a warning is logged. Create a key limited to read on the `tipitaka` bucket. |

Environment (set in `ecosystem.config.cjs`):

| var | default | |
|---|---|---|
| `PORT`, `HOST` | 4321, localhost | |
| `SITE_ALIASES` | – | e.g. `library-v2:library,cloud-v2:cloud` serves the same data under extra prefixes (noindex), for testing a build side by side (needs its own PM2 app + nginx blocks) |
| `LEGACY_DOWNLOAD_MODE` | – | `redirect-page`: browser navigations to `/<id>/download` go to the file page instead of the file |
| `LOG_REQUESTS` | 1 (0 in PM2) | per-request log line; nginx already logs requests in production |
| `DROPLET_SHARE`, `DROPLET_BASE` | 0, – | % of download links served through the droplet (see below); 40 in production |

## Deploying

```bash
./deploy.sh
```
This builds locally, uploads to `~/file-server-v2` on the droplet, runs `npm ci --omit=dev` when the lockfile changed, `pm2 startOrReload`s `file-server-v2` (port 8090) and finishes with a health check. It never overwrites the server's `config/secrets.json` or `data/*.db` (only copies them if missing). After a change in `deploy/nginx/`, also run `ssh -t tipitaka.lk 'sudo nginx -t && sudo systemctl reload nginx'`.

**Server** (`ssh tipitaka.lk`, since 2026-10-04): droplet `new-tipitaka.lk-1gb-sgp1`, Ubuntu 26.04, SGP1 (VPC `default-sgp1`), 1 GB RAM + 2 GB swap, nginx 1.28, behind the Reserved IP `157.230.192.72` that DNS points to (moving a server means reassigning that IP). It also runs the main tipitaka.lk site (`tipitaka_lk` on 8400, `tipitaka_lk_new` on 8401, systemd, from `/var/www/html`). The firewall allows only 22/80/443.
- **nginx:** `/etc/nginx/sites-available/tipitaka.lk.conf` includes [deploy/nginx/file-server-prod.conf](deploy/nginx/file-server-prod.conf) (`/library`, `/cloud`, the Android-app rewrite, the blexbot deny) and [deploy/nginx/file-server.conf](deploy/nginx/file-server.conf) (`/fs-assets/`, `/fs-static/`, `/fs-dl/`).
- **Node:** nvm default 24, PM2 7 started at boot by `pm2-janaka.service`. After upgrading Node with nvm, re-run `pm2 startup` and update the node path in the crontab.
- **Backups:** `crontab -l` runs `scripts/backup.ts` at 03:00 UTC. Snapshots go to `<site>/backups/` in the bucket (private); the last 14 stay in `data/backups/`.
- **TLS:** certbot (systemd timer) renews `tipitaka.lk` and `open.tipitaka.lk`.
- **Bucket CORS:** browser uploads and PDF previews need it. Admin → Bucket → **Check**, then **Set CORS rule**, which adds the current origin to the rule (keep `http://localhost:4321` for dev and the end-to-end test).

## Serving part of the downloads from the droplet

Spaces includes 1 TiB of egress a month and charges $0.01/GiB above that; in September 2026 it sent about 1,750 GiB. The droplets have their own transfer pool (1,000 GiB per $6 droplet, pooled across the team: 2,000 GiB, of which they used about 305 GiB), and [transfer from Spaces in SGP1 to Droplets in SGP1 is free](https://docs.digitalocean.com/platform/billing/bandwidth/). Serving a share of the links through the droplet therefore moves that traffic to the droplet pool.

- **How it works:** the `/fs-dl/` block in `file-server.conf` forwards the signed path and query to Spaces unchanged. It resolves Spaces through the VPC resolver `10.130.255.254`, which answers with the private endpoint. Tested 2026-10-04 with a real Sinhala key: 206, also with `fbclid`; a tampered signature gets 403. After changing the block, check a proxied link with `%`-escapes in its key, not just an ASCII one. Links stay signed and expire as usual, and Node never handles the bytes.
- **Setting:** `DROPLET_SHARE: '40'` in `ecosystem.config.cjs` (on since 2026-10-04). About 42% removes the Spaces overage; more saves nothing. Check the droplet transfer on the Billing page.
- **Limits:** each proxied download uses two nginx connections; the droplet has `worker_connections 4096`, so about 2,000 can run at once.
- **Off:** `DROPLET_SHARE: '0'`, then `./deploy.sh`.

## Notes

- Bucket naming is unchanged: `name[version]{id}.type`, folders `name{id}`. A folder segment without `{id}` makes everything below it unlisted (reachable by id, not listed).
- Files that disappear from the bucket keep their counts and are hidden (`missing_since`) until they reappear.
- An unpublished folder hides its whole subtree. Empty folders stay hidden on the public site.
- Admin can't rename folders that have contents or move files between folders. Do that in the bucket, then sync.
- PDF previews: `<site>/previews/{id}-{n}.jpg` (public), with the page numbers stored in `preview_pages`. Otherwise the old `thumbs/{id}-{0,1}.jpg` are shown; those are page 1 and page `floor(pages/2)+1` (how `thumb-generator.js` made them), labelled once the page count is known.
- Page counts: uploads get them from the browser's pdf.js. `scripts/page-counts.ts` fills in existing PDFs; it uses `pdfjs-dist`, a dev dependency, so run it on your machine, not the server.
