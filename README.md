# File server v2

The download pages of [tipitaka.lk/library](https://tipitaka.lk/library/) and [tipitaka.lk/cloud](https://tipitaka.lk/cloud/), with an admin for the files in the `tipitaka` Spaces bucket.

A Go server (`server/`, one static binary) with SQLite (`modernc.org/sqlite`, pure Go) and `html/template` pages. One process serves `/library` and `/cloud` and uses about 35 MB. Node is only needed on your machine, to build the browser assets (esbuild, Tailwind 4) and run the end-to-end test. Until 2026-10-05 this was an Astro/Node server; [docs/go-rewrite.md](docs/go-rewrite.md) describes the port and how it was checked page for page against the old server.

Design and reasoning: [docs/design.md](docs/design.md). UI parity rules and the download pitfalls it avoids: [docs/design-concerns.md](docs/design-concerns.md).

Downloads never pass through the server. File pages link straight to **SigV2-signed** Spaces CDN URLs. Unlike SigV4, these keep working when Facebook and other apps append `fbclid`. Links expire in 6–12 h, so the only lasting links to a file are our own pages, and those count every download.

## Layout

```
config/            library.json, cloud.json (site settings), secrets.json (git-ignored, see secrets.example.json)
data/              <site>.db SQLite files (git-ignored)
server/            Go server: web.go (routes), store.go (in-memory index + search), sign.go (SigV2 links),
                   admin.go, sync.go, s3.go, db.go, collate.go (Sinhala sort order), templates/ (pages)
server/testdata/   expected outputs captured from the old Node code (formatting, sanitizer, signing, sort order)
src/client/        browser code: site.ts (public pages), admin.ts (loads pdf.js on demand), lightbox.ts
src/styles/        Tailwind CSS (global.css for the public pages, admin-bundle.css for the admin)
scripts/           build-assets.mjs (browser assets -> server/dist), gen-icons.mjs (server/icons.go from @mdi/js)
deploy/nginx/      nginx config, included from the droplet's site config
deploy.sh          build + upload + restart on the droplet
test/e2e/          browser end-to-end suite
```

## Commands

```bash
npm install                         # build tools (needs Go 1.26+ and Node 22.18+)
npm run build                       # browser assets -> server/dist, binary -> server/bin/file_server
npm run dev                         # build and serve on http://localhost:4321/library/ (uses ./config and ./data)
npm test                            # Go unit tests
npm run check                       # type check the browser code
npm run test:e2e                    # admin end-to-end in headless Chrome against the real bucket (see below)
npm run icons                       # after changing the icon list in scripts/gen-icons.mjs
./deploy.sh                         # deploy to the droplet (see below)

server/bin/file_server sync library --dry-run   # bucket -> SQLite preview (also in admin -> Bucket)
server/bin/file_server sync library             # keeps downloads and metadata
server/bin/file_server backup                   # snapshot both DBs to <site>/backups/ in the bucket (nightly cron)
server/bin/file_server snapshot from.db to.db   # consistent copy of a live database
```

## End-to-end test

`npm run test:e2e` drives the admin in headless Chrome against the real bucket, in about a minute. It starts `server/bin/file_server` on :4321 with this repo's `config/` and `data/` if nothing is running there. It covers:
- creating, renaming, publishing and deleting folders;
- uploading a PDF and a text file;
- the name and key rules;
- browser-rendered and re-rendered previews;
- renaming files in the bucket;
- the public pages and signed links (with `fbclid`);
- deleting everything again, then checking nothing is left in the bucket.

It needs `npm run build`, `config/secrets.json`, Google Chrome (or `CHROME=/path`), and the bucket CORS rule for `http://localhost:4321`. All its entries are named `fs-v2 e2e…` and are removed at the end, even after a failure. On a failure it saves a screenshot to `$TMPDIR/fs-e2e-failure.png`.

## Configuration

`config/secrets.json`:

| key | used for |
|---|---|
| `adminPassword` | `/<site>/admin/login` |
| `sessionSecret` | signs the admin session cookie (long random string) |
| `s3` | read/write Spaces key: sync, admin uploads/renames/deletes, backups |
| `signer` | **read-only** Spaces key for public download links. If it's missing, `s3` is used and a warning is logged. Create a key limited to read on the `tipitaka` bucket. |

Environment (set in [server/file_server.service](server/file_server.service) on the droplet):

| var | default | |
|---|---|---|
| `PORT`, `HOST` | 4321, localhost | 8090, 127.0.0.1 on the droplet |
| `APP_ROOT` | working directory | holds `config/` and `data/` |
| `DIST_DIR` | `<root>/dist`, else `<root>/server/dist` | built assets |
| `SITE_ALIASES` | – | e.g. `library-v2:library,cloud-v2:cloud` serves the same data under extra prefixes (noindex), for testing a build side by side |
| `LEGACY_DOWNLOAD_MODE` | – | `redirect-page`: browser navigations to `/<id>/download` go to the file page instead of the file |
| `LOG_REQUESTS` | 1 (0 on the droplet) | per-request log line; nginx already logs requests in production |
| `DROPLET_SHARE`, `DROPLET_BASE` | 0, – | % of download links served through the droplet (see below); 40 in production |
| `GOMEMLIMIT` | – | 60MiB on the droplet: the Go GC collects harder near it |

Search uses [`github.com/pnfo/singlish-search`](https://github.com/pnfo/singlish-search) (Go port, same results as the JS package). To update it: `go get -C server github.com/pnfo/singlish-search@vX.Y.Z`, then `npm test` and `./deploy.sh`.

## Deploying

```bash
./deploy.sh
```
This runs the Go tests, builds the assets and a linux/amd64 binary, and uploads them to `~/www/file_server` (`/var/www/html/file_server`) on the droplet. It keeps the previous binary as `file_server.prev` and restarts the `file_server` systemd unit with sudo (it asks for your password). It finishes with a health check and prints the rollback command if that fails. It never overwrites the server's `config/secrets.json` or `data/*.db`.
- After changing `server/file_server.service`: `ssh -t tipitaka.lk 'sudo cp ~/www/file_server/file_server.service /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl restart file_server'`.
- After changing `deploy/nginx/`: `ssh -t tipitaka.lk 'sudo nginx -t && sudo systemctl reload nginx'`.
- Logs: `ssh tipitaka.lk 'journalctl -u file_server -n 100'`.

**Server** (`ssh tipitaka.lk`, since 2026-10-04): droplet `new-tipitaka.lk-1gb-sgp1`, Ubuntu 26.04, SGP1 (VPC `default-sgp1`), 1 GB RAM + 2 GB swap, nginx 1.28. It sits behind the Reserved IP `157.230.192.72` that DNS points to, so moving a server means reassigning that IP. It also runs the main tipitaka.lk site and the other Go services, each a systemd unit with its directory in `/var/www/html` (`~/www`). The firewall allows only 22/80/443.
- **nginx:** `/etc/nginx/sites-available/tipitaka.lk.conf` includes two files from `~/www/file_server/nginx/`:
  - [file-server-prod.conf](deploy/nginx/file-server-prod.conf): `/library`, `/cloud`, the Android-app rewrite, the blexbot deny;
  - [file-server-go.conf](deploy/nginx/file-server-go.conf): `/fs-assets/`, `/fs-static/`, `/fs-dl/`.
- **Backups:** `crontab -l` runs `./file_server backup` at 03:00 UTC (log: `~/backup.log`). Snapshots go to `<site>/backups/` in the bucket (private); the last 14 stay in `data/backups/`.
- **TLS:** certbot (systemd timer) renews `tipitaka.lk` and `open.tipitaka.lk`.
- **Bucket CORS:** browser uploads and PDF previews need it. Admin → Bucket → **Check**, then **Set CORS rule**, which adds the current origin to the rule (keep `http://localhost:4321` for dev and the end-to-end test).

## Serving part of the downloads from the droplet

Spaces includes 1 TiB of egress a month and charges $0.01/GiB above that; in September 2026 it sent about 1,750 GiB. The droplets have their own transfer pool (1,000 GiB per $6 droplet, pooled across the team: 2,000 GiB, of which they used about 305 GiB), and [transfer from Spaces in SGP1 to Droplets in SGP1 is free](https://docs.digitalocean.com/platform/billing/bandwidth/). Serving a share of the links through the droplet therefore moves that traffic to the droplet pool.

- **How it works:** the `/fs-dl/` block in `file-server-go.conf` forwards the signed path and query to Spaces unchanged. It resolves Spaces through the VPC resolver `10.130.255.254`, which answers with the private endpoint. Tested 2026-10-04 with a real Sinhala key: 206, also with `fbclid`; a tampered signature gets 403. After changing the block, check a proxied link with `%`-escapes in its key, not just an ASCII one. Links stay signed and expire as usual, and the file server never handles the bytes.
- **Setting:** `DROPLET_SHARE=40` in `server/file_server.service` (on since 2026-10-04). About 42% removes the Spaces overage; more saves nothing. Check the droplet transfer on the Billing page.
- **Limits:** each proxied download uses two nginx connections; the droplet has `worker_connections 4096`, so about 2,000 can run at once.
- **Off:** set `DROPLET_SHARE=0` in the unit, install it and restart (see Deploying).

## Notes

- Bucket naming is unchanged: `name[version]{id}.type`, folders `name{id}`. A folder segment without `{id}` makes everything below it unlisted (reachable by id, not listed).
- Files that disappear from the bucket keep their counts and are hidden (`missing_since`) until they reappear.
- An unpublished folder hides its whole subtree. Empty folders stay hidden on the public site.
- Admin can't rename folders that have contents or move files between folders. Do that in the bucket, then sync.
- PDF previews: `<site>/previews/{id}-{n}.jpg` (public), with the page numbers stored in `preview_pages`. Otherwise the old `thumbs/{id}-{0,1}.jpg` are shown; those are page 1 and page `floor(pages/2)+1` (how `thumb-generator.js` made them), labelled once the page count is known.
- Page counts: uploads get them from the browser's pdf.js. Every existing PDF was filled in by the old `scripts/page-counts.ts` (in git history).
- Sort order: listings use ICU's Sinhala order (`Intl.Collator('si')`), reproduced in [server/collate.go](server/collate.go). The test checks it against strings sorted by ICU.
