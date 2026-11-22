# File server v2

The download pages of [tipitaka.lk/library](https://tipitaka.lk/library/) and [tipitaka.lk/cloud](https://tipitaka.lk/cloud/), and a token API to manage the files in two Cloudflare R2 buckets (`tipitaka-library`, private; `tipitaka-public`, public). Until 2026-11 the files were in a DigitalOcean Spaces bucket.

A Go server (`server/`, one static binary) with SQLite (`modernc.org/sqlite`, pure Go) and `html/template` pages. One process serves `/library` and `/cloud` and uses about 35 MB. Node is only needed on your machine, to build the browser assets (esbuild, Tailwind 4) and run the end-to-end test. Until 2026-10-05 this was an Astro/Node server; [docs/go-rewrite.md](docs/go-rewrite.md) describes the port and how it was checked page for page against the old server.

Files are managed only through the token API: [docs/api.md](docs/api.md) (`uv run tools/fsapi.py ...`): view, add, replace, edit, move, delete, sync. The browser admin was removed in 2026-11.

Design and reasoning: [docs/design.md](docs/design.md). UI parity rules and the download pitfalls it avoids: [docs/design-concerns.md](docs/design-concerns.md).

Downloads never pass through the server, and R2 charges nothing for egress.

- **Library** files are private. File pages link to **signed** URLs on `library.tipitaka.lk`, where a small Worker ([worker/](worker/src/index.ts)) checks the signature and streams the object. Only the path, the expiry and the saved filename are signed, so the links keep working when Facebook and other apps append `fbclid`. Links expire in 6–12 h, so the only lasting links to a book are our own pages, and those count every download.
- **Cloud** files are public: plain permanent URLs on `cdn.tipitaka.lk`, the public bucket's own domain (no Worker). A saved file keeps its key name (`name{id}.mp3`), and downloads that other sites link to directly are not counted.

## Layout

```
config/            library.json, cloud.json (site settings), secrets.json (git-ignored, see secrets.example.json)
data/              <site>.db SQLite files (git-ignored)
server/            Go server: web.go (routes), store.go (in-memory index + search), sign.go (download links),
                   admin.go + api.go (the token API), sync.go, s3.go, db.go, collate.go (Sinhala sort order), templates/ (pages)
server/testdata/   expected outputs captured from the old Node code (formatting, sanitizer, sort order);
                   link_vectors.json: signed links, checked by the Go tests and the Worker test
worker/            Cloudflare Worker for library.tipitaka.lk (src/index.ts), its test (test.mjs), wrangler.jsonc
src/client/        browser code: site.ts (public pages), lightbox.ts
src/styles/        Tailwind CSS (global.css)
scripts/           build-assets.mjs (browser assets -> server/dist), gen-icons.mjs (server/icons.go from @mdi/js)
deploy/nginx/      nginx config, included from the droplet's site config
deploy.sh          build + upload + restart on the droplet
tools/fsapi.py     command-line client for the token API (docs/api.md)
test/e2e/          end-to-end test of the token API (api.e2e.py)
```

## Commands

```bash
npm install                         # build tools (needs Go 1.26+ and Node 22.18+)
npm run build                       # browser assets -> server/dist, binary -> server/bin/file_server
npm run dev                         # build and serve on http://localhost:4321/library/ (uses ./config and ./data)
npm test                            # Go unit tests
npm run check                       # type check the browser code
npm run test:e2e                    # token API end-to-end against the real bucket (see below)
npm run icons                       # after changing the icon list in scripts/gen-icons.mjs
./deploy.sh                         # deploy to the droplet (see below)

server/bin/file_server sync library --dry-run   # bucket -> SQLite preview (also: fsapi.py sync)
server/bin/file_server sync library             # keeps downloads and metadata
server/bin/file_server backup                   # snapshot both DBs to <site>/backups/ in the bucket (nightly cron)
server/bin/file_server snapshot from.db to.db   # consistent copy of a live database
```

## End-to-end test

`npm run test:e2e` runs [test/e2e/api.e2e.py](test/e2e/api.e2e.py): every `fsapi.py` command against a local server and the real library bucket, in under a minute. It starts `server/bin/file_server` on :4321 with this repo's `config/` and `data/` if nothing is running there. It covers:
- making folders, adding a PDF (page count, previews) and a text file;
- the public page, the signed link (with `fbclid`) and the public preview image;
- rename, move, publish, replace (with its backup), editions, search, download, sync;
- deleting everything again, then checking nothing is left in the bucket.

It needs `npm run build`, `config/secrets.json`, `uv`, and a recent copy of the server's database in `data/library.db` (the script's header says why and how). All its entries are named `fs-v2 e2e…` and are removed at the end, even after a failure.

## Configuration

`config/secrets.json`:

| key | used for |
|---|---|
| `s3` | R2 API token (Object Read & Write on both buckets) and `endpoint` (`<account id>.r2.cloudflarestorage.com`): sync, API uploads/renames/moves/deletes, backups |
| `linkSecret` | signs library download links. The Worker's `LINK_SECRET` must hold the same value. |

`config/library.json` and `config/cloud.json` name each site's `bucket` and `publicHost`, and say whether its links are `signed`.

Environment (set in [server/file_server.service](server/file_server.service) on the droplet):

| var | default | |
|---|---|---|
| `PORT`, `HOST` | 4321, localhost | 8090, 127.0.0.1 on the droplet |
| `APP_ROOT` | working directory | holds `config/` and `data/` |
| `DIST_DIR` | `<root>/dist`, else `<root>/server/dist` | built assets |
| `SITE_ALIASES` | – | e.g. `library-v2:library,cloud-v2:cloud` serves the same data under extra prefixes (noindex), for testing a build side by side |
| `LEGACY_DOWNLOAD_MODE` | – | `redirect-page`: browser navigations to `/<id>/download` go to the file page instead of the file |
| `LOG_REQUESTS` | 1 (0 on the droplet) | per-request log line; nginx already logs requests in production |
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
  - [file-server-go.conf](deploy/nginx/file-server-go.conf): `/fs-assets/`, `/fs-static/`.
- **Backups:** `crontab -l` runs `./file_server backup` at 03:00 UTC (log: `~/backup.log`). Snapshots go to the private bucket (`library/backups/`, `cloud-db-backups/`); the last 14 stay in `data/backups/`.
- **TLS:** certbot (systemd timer) renews `tipitaka.lk` and `open.tipitaka.lk`.
- **Bucket CORS:** `tipitaka-public` allows GET/HEAD from any origin, for the audio timing files the apps fetch. Both buckets also still allow GET/PUT/HEAD from `https://tipitaka.lk` and `http://localhost:4321`, which only the removed browser admin needed. The rules are set in the Cloudflare dashboard (R2 → bucket → Settings → CORS policy) or API; the `s3` token cannot change them.

## Cloudflare

DNS for `tipitaka.lk` is on Cloudflare (free plan). Every record of the droplet is **DNS only**; just the two file hostnames go through Cloudflare.

| hostname | serves | how |
|---|---|---|
| `library.tipitaka.lk` | bucket `tipitaka-library`: `library/…`, and the DB snapshots of both sites | the Worker in [worker/](worker/src/index.ts): signed links; `library/previews/` and `library/thumbs/` are public; snapshots are never served |
| `cdn.tipitaka.lk` | bucket `tipitaka-public`: `cloud/…`, and the BJT audio in `bjt-audio/…` | the bucket's public custom domain |

- **Worker:** `npm run deploy:worker` (wrangler, needs `CLOUDFLARE_API_TOKEN`). Its secret: `cd worker && npx wrangler secret put LINK_SECRET`, the same value as `linkSecret` in `config/secrets.json`. To change the secret, set it in both places; links signed with the old one stop working at once.
- **Free plan limit:** 100,000 Worker requests a day (every library download and preview image is one; a PDF opened in a browser viewer can be several Range requests). Past the limit requests fail until 00:00 UTC. Workers → the Worker → Metrics shows the daily count; the paid plan is $5/month.
- **Save to device on cloud:** `cdn.tipitaka.lk` has one response-header Transform Rule: when the query string contains `dl=1`, set `Content-Disposition: attachment`. The file server adds `?dl=1` to cloud download buttons.
- **Snapshots:** `library/backups/` and `cloud-db-backups/` are in the private bucket. Nothing under `cloud/` is private: copies made by `replace` and token `rm` (`cloud/backups/…`) are as public as the files they copy.
- **Health:** `/<site>/api/health` fetches one byte of a real link of the site, with and without `fbclid`, and answers 503 if either fails.

## Notes

- Bucket naming is unchanged: `name[version]{id}.type`, folders `name{id}`. A folder segment without `{id}` makes everything below it unlisted (reachable by id, not listed).
- Files that disappear from the bucket keep their counts and are hidden (`missing_since`) until they reappear.
- An unpublished folder hides its whole subtree. Empty folders stay hidden on the public site.
- A file (or an empty folder) can be moved to another folder (`fsapi.py meta ID --parent N`); the object moves in the bucket, the id and link stay. Folders that have contents can't be renamed or moved that way: do that in the bucket, then sync.
- Editions: files that are editions of one work share `edition_group` (the smallest id in the group), and each file page lists the others. Set with the API `editions` action (`{ids: [...]}` merges, `{ids: [id], clear: true}` takes one out). Sync never changes it.
- PDF previews: `<site>/previews/{id}-{n}.jpg` (public), with the page numbers stored in `preview_pages`. Otherwise the old `thumbs/{id}-{0,1}.jpg` are shown; those are page 1 and page `floor(pages/2)+1` (how `thumb-generator.js` made them), labelled once the page count is known.
- Page counts: uploads get them from the browser's pdf.js. Every existing PDF was filled in by the old `scripts/page-counts.ts` (in git history).
- Sort order: listings use ICU's Sinhala order (`Intl.Collator('si')`), reproduced in [server/collate.go](server/collate.go). The test checks it against strings sorted by ICU.
