# File Server v2 — System Design

**Project:** tipitaka.lk `/library` and `/cloud` file server
**Status:** In production since 2026-10-04 (this repo; see [README](../README.md)). Written while it was being built in `astro-server/` of the old [pnfo/file-server-new](https://github.com/pnfo/file-server-new) repo, so paths and plans below are from that time.
**Replaces:** the Nuxt 3 app (pnfo/file-server-new) and an earlier unfinished Astro + Vue rewrite
**Read with:** `design-concerns.md`. Its UI parity rules (concerns 1–12 and §9 rules 2–5, 7) still apply. This document replaces its §10 (downloads) and drops the Vue-island and server-side PDF rendering parts.

---

## 1. Decisions at a glance

| # | Decision | Replaces |
|---|----------|----------|
| D1 | **Files stay private. Links are short-lived SigV2 query-signed URLs** (`AWSAccessKeyId`/`Expires`/`Signature`). Extra query params such as `fbclid` don't affect them (verified, §2). | SigV4 presigned URLs, which break when `fbclid` is appended |
| D2 | **The file page links straight to the signed file URL.** No interstitial page, no JS/meta redirect. | The HTML "bridge" page (`location.replace` + `meta refresh`) |
| D3 | **Downloads are counted on our side only.** A `sendBeacon` hit from our file page, or a server-side count on `/download`. Other sites can't link to a file permanently, because signed links expire. They have to link to `/{id}` or `/{id}/download`, and both are counted. | Counting in the `/download` handler only |
| D4 | **The saved filename and inline vs attachment are set per link** (`response-content-disposition`, signed). No metadata change to stored objects. | Object paths with `[ver]{id}` appearing as the saved filename |
| D5 | **In-app browsers (Facebook, Instagram, Messenger, etc.) are detected** and shown an "open in Chrome/Safari" helper next to the normal buttons. | Hoping the bridge page works in webviews |
| D6 | **Server-rendered HTML with no client framework on public pages.** Sorting and live search are small vanilla scripts that work on the existing DOM. | Vue islands (the cause of the hydration mismatch bug) |
| D7 | **PDF previews and page counts are made in the admin's browser** (pdf.js) and uploaded straight to Spaces. The droplet never parses a PDF. | `pdfjs-dist` + `@napi-rs/canvas` + `SafePath2D` on the 280 MB droplet |
| D8 | **One Node process serves both sites** (`/library`, `/cloud`), with one SQLite file per site. | One PM2 process per site |
| D9 | **S3 stays the source of truth for which files exist** (the `name[version]{id}.type` naming is kept). SQLite holds everything else. | Two competing truths (S3 listing vs DB) |

Stack: **Node 24 LTS · Astro 7 (SSR, `@astrojs/node`) · Tailwind CSS 4 · node:sqlite (built in, plain SQL) · `@pnfo/singlish-search@1.2.5` (exact) · nginx · PM2.**

---

## 2. Why the current download flow keeps breaking

What we found in the code and the bucket. Each test ran against a real library PDF on 2026-10-01 with a 100-byte `Range` request.

| URL kind | Plain | With `&fbclid=IwAR…` appended |
|---|---|---|
| SigV4 presigned (current production) | 206 | **403 `SignatureDoesNotMatch`** |
| SigV2 query-signed, origin endpoint | 206 | **206** |
| SigV2 query-signed, CDN endpoint | 206 | **206** |
| SigV2 + signed `response-content-disposition` (inline or attachment, Sinhala `filename*`) | 206, header applied | **206, header applied** |
| Public object (thumbnails) | 200 | 200 |

- SigV4 signs the whole query string, so any parameter a webview adds invalidates it. SigV2 signs only the path, the expiry and specific sub-resources (`response-*`). Unknown params are ignored. **This is the whole Facebook `SignatureDoesNotMatch` bug, and D1 removes it without making files public.**
- The bridge page adds a history entry that navigates itself away. If the file opens in the same tab, Back lands on the bridge, which redirects again. If the file downloads instead (Android), the user is left on a spinner page. In either case Back doesn't return to the file page. **This is the "hard to navigate back" problem. Any auto-redirecting interstitial will have it, however it's tuned. D2 removes the interstitial.**
- The `astro-server` download route went back to a plain `302` to a SigV4 URL. It has every original problem.
- Book/audio objects are private (owner-only ACL) and have the right `Content-Type`. Thumbnails are public. The bucket has no CORS configuration.

### 2.1 Alternatives considered

| Option | Verdict |
|---|---|
| Public objects + direct CDN links | Rejected: other sites could link to files directly, and those downloads wouldn't be counted |
| SigV4 + in-app browser detection only | Rejected: any handoff that appends params still breaks, and we can't control every webview |
| nginx `secure_link` + `proxy_pass` to Spaces (same-origin `/f/…` URLs, counted from the nginx log) | Works. Kept as **fallback 1** if Spaces ever drops SigV2. Cost: every byte goes through the droplet's bandwidth allowance. |
| Cloudflare Worker + R2 at `files.tipitaka.lk` (counts every request at the edge, zero egress fees) | Best long-term option, but it means moving DNS from DigitalOcean and migrating the data. **Fallback 2.** |

---

## 3. Constraints carried forward

- Droplet: Node heap capped near 280 MB. Never stream file bodies through Node (`design-concerns.md` §10.2 C).
- Files up to ~300 MB: PDFs, MP3/M4A, ZIP, APK, HTML, DOCX, XLSX, JPG, STL, AI, DB.
- Scale today: library ≈ 1,170 files / 34 folders; cloud ≈ 2,640 files / 134 folders. Everything fits in memory easily.
- Existing links must keep working: `/library/{id}`, `/library/{id}/download`, `/search/{q}`, `/recent/{days}`, and the same under `/cloud`.
- Every download must be counted. Direct links to file bytes that bypass us must not keep working (they expire).
- Sinhala UI, UN-Abhaya font, Singlish search behaviour must not change.

---

## 4. Architecture

```
                         ┌───────────────────────────────────────────┐
                         │ DigitalOcean Spaces  bucket "tipitaka"    │
                         │  library/…/name[ver]{id}.pdf  (private)   │
                         │  library/previews/{id}-{n}.jpg (public)   │
                         │  cloud/…                                  │
                         └──────▲───────────────────▲────────────────┘
  GET via CDN, SigV2-signed     │                   │ presigned PUT (admin only)
  link, range requests ok       │                   │ ListObjectsV2 (sync)
┌─────────────────────┐         │                   │
│ Browser / webview   │─────────┘                   │
│  HTML + ~3 KB JS    │                             │
│  sendBeacon(hit)    │──┐                          │
└─────────────────────┘  │ HTML, /api/hit           │
                         ▼                          │
                  ┌─────────────┐   proxy   ┌───────┴────────────────────┐
                  │   nginx     │──────────▶│ Node: Astro SSR (1 process) │
                  │ static/_astro│          │  in-memory index per site   │
                  │ gzip, cache │           │  SigV2 link signer          │
                  └─────────────┘           │  node:sqlite (WAL)          │
                                            │   data/library.db           │
                                            │   data/cloud.db             │
                                            └────────────────────────────┘
```

File bytes go from the Spaces CDN to the user and never pass through the droplet. Node renders HTML (with signed links in it), records hits, and handles admin metadata.

---

## 5. File delivery (the core of this redesign)

### 5.1 The link signer (`src/lib/sign.ts`, ~25 lines, no SDK)

```ts
// SigV2 query-string auth. Survives appended params like fbclid (§2).
export function fileLink(site, entry, { mode = 'inline', now = Date.now() } = {}) {
  const path    = `/${BUCKET}/${[site, ...entry.s3Key.split('/')].map(encodeURIComponent).join('/')}`
  const expires = windowEnd(now)                       // see "CDN-friendly expiry" below
  const cd      = `${mode}; filename="${asciiName(entry)}"; filename*=UTF-8''${encodeURIComponent(cleanName(entry))}`
  const sts     = `GET\n\n\n${expires}\n${path}?response-content-disposition=${cd}`   // value NOT url-encoded in the string-to-sign
  const sig     = createHmac('sha1', SECRET).update(sts).digest('base64')
  return `https://${BUCKET}.sgp1.cdn.digitaloceanspaces.com${path.slice(BUCKET.length + 1)}`
       + `?AWSAccessKeyId=${KEY_ID}&Expires=${expires}&Signature=${encodeURIComponent(sig)}`
       + `&response-content-disposition=${encodeURIComponent(cd)}`
}
```

- `cleanName(entry)` is `name` + (`-version` if present) + `.type`. The saved file gets a clean Sinhala name, not `…[1965]{200}.pdf`.
- **CDN-friendly expiry:** `windowEnd(now)` = the end of the next full 6-hour UTC window (so links live between 6 and 12 h). Everyone in a window gets the *identical* URL for a file, so the Spaces CDN can cache it instead of treating each visitor's URL as a new object.
- Use a **dedicated read-only Spaces key** for signing, not the admin key. It must be a separate key pair because SigV2 uses HMAC-SHA1 with the secret.
- Golden test: sign a fixed key/time and compare with a known-good URL. Also a startup self-check (HEAD on one object, logs a warning if Spaces stops accepting SigV2).

### 5.2 The file page (`/{site}/{id}`)

The only screen that starts a download. Everything is a plain `<a href>`, so it works with no JS, and Back always returns here.

| Element | `href` | Notes |
|---|---|---|
| Primary button (`typeInfo.downloadText`, e.g. "PDF ගොනුව බාගත කරගන්න") | `fileLink(…, {mode:'inline'})` | Same tab. Desktop opens the browser's viewer (which has a save button). Mobile Chrome/Safari open their viewer or download. **No redirect.** |
| Secondary link "උපාංගයට සුරකින්න / Save to device" (PDF, HTML, images) | `fileLink(…, {mode:'attachment'})` | Forces a download. The browser stays on the file page. |
| Audio player (mp3/m4a) | `<audio controls preload="none" src="{inline link}">` | Direct CDN range requests. `preload="none"`, so loading the page does not pull a 60 MB sermon. |
| Share button | — | Unchanged (copies `webUrlRoot + id`, never the signed link) |

- Response header for file pages: `Cache-Control: private, max-age=300`. Signed links outlive any cached copy of the page.
- **Stale-page guard** (`hit.js`): each link carries `data-expires`. On click, if it is within 10 minutes of expiring (for example a tab left open overnight, or a back/forward-cache restore), `preventDefault()`, fetch a fresh link from `GET /api/link/{id}?mode=…`, and navigate to it. This is an in-page `location.href` change, not an interstitial, so history is unchanged. Without JS, the worst case is one expired link on a page left open for 6+ hours. Reloading fixes it.

### 5.3 Counting downloads

```js
// public/js/hit.js (~30 lines incl. the stale guard). Loaded on file pages only.
document.addEventListener('click', e => {
  const a = e.target.closest('a[data-hit]'); if (!a) return
  navigator.sendBeacon(`${base}/api/hit/${a.dataset.hit}`)   // fire-and-forget, survives navigation
})
audio?.addEventListener('play', () => navigator.sendBeacon(...), { once: true })
```

- `POST /api/hit/:id` returns `204` and increments `downloads` in SQLite. A small in-memory LRU ignores repeats of the same `(ip, id)` within 10 minutes. The `/download` route uses the same rule.
- **No route to the bytes stays usable without passing through us.** Our pages hand out links that expire within 12 h. Other sites can embed either `/{id}` (file page, counted on click) or `/{id}/download` (counted on the server). Copying a signed link from devtools gives a link that dies the same day.
- Not counted: no-JS users of the file page (small) and repeat uses of the same signed link within its window (for example a download manager resuming). Both are acceptable.

### 5.4 Legacy `/{site}/{id}/download` (old shared links, external embeds)

Kept forever. **Count the hit, then `302` straight to `fileLink(…, {mode:'inline'})`.** That's safe now, even in Facebook's browser: if the webview appends `fbclid` while handing off to Chrome, the SigV2 link still works. A 302 leaves no history entry, so Back works. **No HTML bridge.**

Fallback if the Phase 0 tests (§10) still show the mobile Chrome "Loading PDF" stall on a cross-origin 302: send top-level navigations (`Sec-Fetch-Dest: document`) to the file page instead, with `<audio>`/curl/embeds still getting the 302. That's a one-line switch controlled by `LEGACY_DOWNLOAD_MODE=redirect-file|redirect-page`.

### 5.5 In-app browsers

The `fbclid` breakage is gone (D1), but webviews are still poor at PDFs. Android FB can't render them inline, and iOS FB renders them with no obvious save option. So the file page adds a helper, without removing the normal buttons.

Detect on the server from the User-Agent: `FBAN|FBAV|FB_IAB|FBIOS|Instagram|Messenger|Line/|MicroMessenger|Snapchat|TikTok|musical_ly`, configurable.

- **Android:** a prominent button "Chrome එකෙන් විවෘත කරන්න / Open in Chrome" →
  `intent://tipitaka.lk/{site}/{id}#Intent;scheme=https;package=com.android.chrome;S.browser_fallback_url={encoded file page URL};end`
  This opens the **file page** in Chrome, so the user lands somewhere normal with working buttons and Back.
- **iOS:** text "⋯ → Open in Safari" next to a "copy link" button. Also try `x-safari-https://…` behind a button (works in recent iOS; harmless if ignored).

### 5.6 Behaviour matrix (target)

| Environment | PDF tap | Back afterwards | Audio |
|---|---|---|---|
| Desktop Chrome/Edge/Firefox/Safari | Opens in built-in viewer, saves with the clean name | Returns to file page | Plays, seeks |
| Android Chrome | Opens viewer or downloads to Downloads | Returns to file page / stays on it | Plays, seeks |
| iOS Safari | Opens in viewer, Share → Save to Files | Returns to file page | Plays, seeks |
| Facebook / Instagram (Android) | The download is handed to Chrome or the system downloader and **works even with `fbclid`**. "Open in Chrome" is also offered. | Stays on file page in FB | Plays (direct CDN) |
| Facebook / Instagram (iOS) | Renders inline (WKWebView); "Open in Safari" hint shown | Returns to file page | Plays |
| Old link `/id/download` in any normal browser | Counted, 302 → file | Returns to the referring page | — |
| Another site linking a signed URL it copied | Works until the link expires (≤ 12 h), then 403 | — | — |

---

## 6. Application design

### 6.1 Routes (one Astro app, site chosen from the first path segment)

nginx sends `/library/*` and `/cloud/*` to the same port. Astro `base` is `/`. Pages live under `src/pages/[site]/…`. Middleware resolves `site` → `{config, db, index}` and returns 404 for unknown sites.

| Route | Type | Purpose |
|---|---|---|
| `/{site}/` | page | Root folders (Name + Size columns only, concern 7) |
| `/{site}/{id}` | page | Folder view or file view |
| `/{site}/search/{q}` and `/{site}/search?q=` | page | Results (with the parent folder column, without date added, concern 6) |
| `/{site}/recent/{days}` | page | Recently added |
| `/{site}/{id}/download` | 302 | Legacy (§5.4) |
| `/{site}/api/link/{id}?mode=inline\|attachment` | JSON | Fresh signed link for the stale-page guard (§5.2). Rate-limited per IP. |
| `/{site}/api/hit/{id}` | POST 204 | Counter (§5.3) |
| `/{site}/api/search?q=` | HTML fragment | Live search `<tbody>` (§6.3) |
| `/{site}/admin/*` | pages + JSON | Admin (§7) |

Response headers: folder/search/recent pages get `Cache-Control: public, max-age=60` (nginx can microcache them). File pages, which contain signed links, get `private, max-age=300`. `_astro/*` and fonts get `immutable`. Pages for unpublished entries return 404.

### 6.2 Front-end: server HTML, enhanced in place

- **No Vue/React on public pages.** Every page renders as complete HTML on the server. That removes the hydration-mismatch bug class (`design-concerns.md` §4.1) entirely, rather than guarding against it.
- **Sorting:** `sort.js` (~40 lines) reads `data-name/size/downloads/date` on each `<tr>`, sorts with `Intl.Collator('si')`, and re-appends the rows. It runs only when a header is clicked. The initial order is the server's (by name).
- **Icons:** an inline SVG sprite of the ~14 MDI icons in use, instead of the full MDI webfont (~400 KB). Colours as in concern 3.
- **Font:** UN-Abhaya converted to `woff2`, `font-display: swap`, preloaded.
- **Budget:** under 15 KB of JS per page, under 30 KB of CSS, no layout shift. A Lighthouse mobile score of 95+ is an acceptance criterion.
- The UI parity rules in `design-concerns.md` concerns 1–12 are the visual acceptance checklist. Reuse the markup/classes from `astro-server/src/components/*.astro` where they already pass it.

### 6.3 Search

- At startup, each site's entries are loaded into memory (a few thousand rows). Search works as it does in production today: `getPossibleMatches(q)` (exact `1.2.5`), capped at 300 terms, plus the raw query. Build one escaped-alternation `RegExp` and test it against `name`, `version`, `author`, `description` for files and folders. Return at most 100 results.
- **Focus problem solved structurally:** the search box is a GET form (works without JS). With JS, `search.js` debounces input (250 ms), fetches `/api/search?q=` (server-rendered `<tbody>` HTML), swaps it in, and calls `history.replaceState` to `/search/{q}`. The input is never re-rendered, so focus and caret stay where the user left them. `autofocus` covers the no-JS full-page case.

### 6.4 In-memory index + SQLite

- `index.ts` holds `Map<id, Entry>` and `Map<folderId, childIds[]>` per site, built from SQLite at boot and rebuilt after any admin write or sync. Every public read is a map lookup. SQLite is only for persistence.
- SQLite with WAL and `synchronous=NORMAL`. Hits use one prepared `UPDATE … SET downloads = downloads + 1`, which is cheap at this volume. No hourly JSON flush and no data lost on crash.
- **No memory-watchdog `process.exit`.** Instead PM2 `max_memory_restart: 250M`, as a safety net only.

---

## 7. Admin

- `/{site}/admin` is protected by a password (from `config/secrets.js`, not in git). It sets an HttpOnly, `SameSite=Strict`, `Secure` session cookie (HMAC-signed, 7 days). Login is rate-limited.
- **Server-rendered pages with small scripts.** If richer widgets are wanted later, Vue may be used in admin only with `client:only` (no SSR, so no hydration).
- Features (same scope as the astro attempt):
  - Browse, search, publish/unpublish, edit metadata (version, author, publishing year, printed year, description), move, delete.
  - **Upload:** the browser asks `POST /admin/api/upload-url` for a presigned PUT, which returns `{key, url}` (SigV4 is fine here: admin-only, never shared) with a private ACL and the right `Content-Type`. Nothing else is needed on the object: filename and disposition are set per link (D4). The browser PUTs the file **directly to Spaces** with a progress bar, then calls `POST /admin/api/commit` to insert the row. Files over 500 MB use multipart presigned parts (rare). Upload bytes never pass through Node.
  - **Previews (D7):** the admin opens the PDF from a signed link in the browser using pdf.js and gets the page count for free. They pick pages from a thumbnail strip (default: page 1 and the middle page, matching `thumb-generator.js`). Each page renders to a canvas at a height of 1500 px, then goes through `toBlob('image/jpeg', 0.8)` and a presigned PUT to `{site}/previews/{id}-{n}.jpg` (`public-read`. Preview images may be public: they are not the file). The page list and `num_pages` are saved to the row. The browser's real font rendering handles the Sinhala Type3 fonts that crashed `@napi-rs/canvas`.
  - **Sync from S3:** a button (and `npm run sync -- --site library`) that lists the bucket, parses `name[version]{id}.type`, upserts rows, flags missing objects and duplicate ids, and reports a diff. This keeps the existing workflow of dropping files into Spaces by hand (Cyberduck etc.) working.
- Bucket CORS (none today; required for browser PUT and pdf.js reads): allow `GET, PUT, HEAD` from `https://tipitaka.lk` (and `http://localhost:4321` for development), with headers `Content-Type, x-amz-acl, Range`. This is needed for admin uploads and pdf.js reads only. Public downloads are plain navigations and need no CORS.

---

## 8. Data model

### 8.1 Schema (one DB file per site: `data/{site}.db`)

```sql
CREATE TABLE entries (
  id            INTEGER PRIMARY KEY,           -- the {id} in the file name
  parent_id     INTEGER NOT NULL DEFAULT 0,    -- 0 = root
  kind          TEXT NOT NULL,                 -- 'folder' | 'file'
  type          TEXT,                          -- pdf, mp3, … (NULL for folders)
  name          TEXT NOT NULL,
  version       TEXT,                          -- the [version] tag
  author        TEXT,
  publishing_year INTEGER,
  printed_year  INTEGER,
  description   TEXT,                          -- sanitised HTML
  s3_key        TEXT,                          -- relative to site root; NULL for folders
  size_bytes    INTEGER NOT NULL DEFAULT 0,    -- folders: computed sum, cached
  num_entries   INTEGER NOT NULL DEFAULT 0,    -- folders: computed, cached
  num_pages     INTEGER,
  preview_pages TEXT,                          -- JSON array of 1-based page numbers
  downloads     INTEGER NOT NULL DEFAULT 0,
  is_published  INTEGER NOT NULL DEFAULT 1,
  date_added    TEXT NOT NULL,                 -- YYYY-MM-DD
  updated_at    TEXT NOT NULL,
  missing_since TEXT                           -- set by sync when the object disappears
);
CREATE INDEX entries_parent ON entries(parent_id);
CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT); -- schema_version, last_sync, …
```

Migrations: numbered `.sql` files applied at boot (`PRAGMA user_version`). No ORM; there are fewer than 15 queries in total.

### 8.2 Source of truth

| Field | Owner |
|---|---|
| Existence, folder placement, `name`, `version`, `id`, `type`, size | **S3 key** (sync reads it; admin writes it through the key) |
| downloads, date_added, author, years, description, previews, published | **SQLite** |

### 8.3 Replacing / renaming a file

The admin uploads the new object under the same `{id}` (its key may differ), updates `s3_key`, and deletes the old object. Downloads and date_added carry over, as the current "reuse the rowid" rule intends. The signed link is built from `s3_key`, so the new file is served right away (the CDN cache key changes with the path).

### 8.4 Backups

A nightly `sqlite3 data/{site}.db ".backup …"` uploaded to `backups/` in the bucket (private), with 30 days kept. This replaces committing `id-to-info.json` to git.

---

## 9. Deployment

- **nginx**
  ```nginx
  location ~ ^/(library|cloud)/_astro/ { alias /srv/file-server/dist/client/_astro/; expires max; }
  location ~ ^/(library|cloud)(/|$)    { proxy_pass http://127.0.0.1:8090; proxy_set_header X-Forwarded-For $remote_addr; }
  ```
  Static assets get copied per site, or served via one shared `/fs-assets/` prefix (`build.assetsPrefix`). No proxy buffering tweaks are needed, because only small HTML passes through.
- **PM2:** one app `file-server`, `NODE_OPTIONS=--max-old-space-size=200`, `max_memory_restart: 250M`. Expected RSS is 60–90 MB for both sites.
- **Secrets:** a dedicated *read-only* Spaces key pair for link signing (§5.1), separate from the admin read/write key. Both live in `config/secrets.js`.
- **Config:** `config/{site}.json` (as today: `fileTypeName`, `rootFolderName`, `webUrlRoot`, `s3RootFolder`, `ogImage`, `displayThumbs`) and `config/secrets.js` (git-ignored).
- **Health:** `GET /library/api/health` returns entry counts, DB ok, and last sync time. Logging: one line per request in JSON via `pino`, no `console.log` of every search.

---

## 10. Plan

**Phase 0 — prove the delivery model on real devices (½ day, before any app code).**
The `fbclid` tolerance of SigV2 is already verified with curl (§2). What's left is browser behaviour. Put a static test page on tipitaka.lk with SigV2 links (inline and attachment) to a 5 MB PDF, a 200 MB PDF and an MP3. Use real Sinhala keys with `{}`/`[]`. Include the §5.5 in-app browser buttons and a legacy-style 302. Share it in a private Facebook post and Messenger chat. Record results in the matrix in §5.6 on: Android Chrome, Android FB/Messenger/Instagram, iOS Safari, iOS FB, desktop Chrome/Safari. **Go/no-go for D1/D2 and the `LEGACY_DOWNLOAD_MODE` default.**

**Phase 1 — read-only public site at `/library-v2`.**
Data import: S3 listing + `id-to-info.json` (downloads, date_added) + extended metadata from `astro-server/db/data/*.db`. Build the folder, file, search and recent pages, then the link signer, hit beacon and legacy route. Check them against the concerns 1–12 checklist side by side with production.

**Phase 2 — no object migration is needed.** Files stay private and unchanged. Only two bucket changes: add the CORS rule (§7) and create the read-only signing key. Move `thumbs/{id}-{0,1}.jpg` → `previews/{id}-{1,2}.jpg`, or just keep reading the old paths.

**Phase 3 — cutover.** Freeze counts on the old app (force-write `id-to-info.json`), import the final counts, then switch nginx `/library` and `/cloud` to the new process. Keep the old PM2 apps stopped but installed for 2 weeks for rollback.

**Phase 4 — admin.** Login, metadata edit, publish/unpublish, sync, then upload, then browser-side previews. Until this ships, the current workflow (upload to Spaces by hand, then sync) still works.

---

## 11. Risks and open questions

| Risk / question | Mitigation |
|---|---|
| DigitalOcean deprecates SigV2 query auth (AWS S3 has for new buckets; Spaces still accepts it as of 2026-10-01) | Startup self-check + health endpoint alert (§5.1). Fallback 1: nginx `secure_link` gateway. Fallback 2: Cloudflare Worker + R2 (§2.1). Both keep the counting guarantees. Only `fileLink()` changes. |
| Someone shares a signed link copied from devtools | It expires within 12 h. Downloads during that window aren't counted (negligible). |
| Many users get the same signed URL within a window | Intended (CDN caching). The link only grants read of that one public-domain file. |
| Mobile Chrome's stall on cross-origin 302 recurs | Phase 0 tests it; `LEGACY_DOWNLOAD_MODE=redirect-page` is ready |
| In-app browser UA strings change | The detection list is one regex in config. Normal buttons are always shown too. |
| Key names with `{}`/`[]`/Sinhala break signing | Per-segment encoding plus a golden test. Already verified with a real Sinhala key (§2). |
| `@pnfo/singlish-search` drift | Exact pin `1.2.5` plus a snapshot test of 20 known queries → expected result ids |

## 12. Rules for whoever implements this

1. Never add an auto-redirecting interstitial page (JS `location`, `meta refresh`) to any download path.
2. File links must be SigV2 (or a fallback from §2.1), never SigV4. They must expire within 12 h and never be shown as a "share" link. Share links are always `webUrlRoot + id`.
3. Never make file objects public. Never stream file bodies through Node.
4. No client framework hydration on public pages. Enhancement scripts act on server-rendered DOM and must leave the page fully usable without JS.
5. Any change to file delivery must re-run the §5.6 matrix on real devices, including the Facebook app.
6. Keep `design-concerns.md` §9 rules 2, 3, 4, 5 and 7 (size unit colours, borderless notices, folder-only columns, search focus, singlish pin).
