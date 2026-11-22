# File API (for scripts and Claude sessions)

The admin's JSON API (`/<site>/admin/api/*`) also accepts a bearer token, so scripts and AI sessions can view, add, replace and edit files without the browser admin. The server code is in `server/api.go`.

## Quick start: `tools/fsapi.py`

```bash
uv run tools/fsapi.py ls                       # library root (FS_API_URL=https://tipitaka.lk/cloud for the cloud site)
uv run tools/fsapi.py ls 1004                  # a folder; unpublished entries are marked
uv run tools/fsapi.py search "විසුද්ධිමග්ග"
uv run tools/fsapi.py get 1222                 # entry JSON + folder path (+ children / edition ids)
uv run tools/fsapi.py download 1222 ~/Downloads
uv run tools/fsapi.py add book.pdf --parent 1004 --name "..." --author "..." --publishing-year 1999 [--unpublished]
uv run tools/fsapi.py replace 1222 fixed.pdf   # new content for an existing entry: same id, key and page URL
uv run tools/fsapi.py meta 1222 --version "2nd ed" --parent 51 --published no
uv run tools/fsapi.py mkdir 1004 "New folder"
uv run tools/fsapi.py previews 1222 book.pdf --pages 1,14
uv run tools/fsapi.py editions 1222 1300        # mark as editions of one work; editions --clear 1222 takes one out
uv run tools/fsapi.py rm 1222
```

Every command prints JSON (`ls` prints a table unless `--json`). For PDFs, `add` and `replace` render preview images and the page count with PyMuPDF, as the admin page does in the browser. `--no-previews` skips that (a `replace` then keeps the old preview images).

The token comes from `FS_API_TOKEN`, or from `apiTokens.claude` in `config/secrets.json`.

## Safety

- **Tokens.** `config/secrets.json` → `"apiTokens": {"claude": "<random, 32+ chars>"}`. Use one name per user of the API. To revoke a token, remove it and restart the server. Tokens shorter than 32 characters are ignored. Make one with `python3 -c "import secrets; print(secrets.token_urlsafe(32))"`.
- **Scope.** A token opens `/admin/api/*` only, never the HTML admin pages. It does not fall back to the cookie.
- **Rate limit.** A wrong token counts as a failed login. After 5 failures, that IP is blocked for 15 minutes.
- **Backups.** `replace` copies the current object to `<site>/backups/replaced/<time>-<file name>` first. A token `rm` of a file copies it to `backups/deleted/`. Sync ignores `backups/`. Deletes from the admin page don't make a backup.
- **Audit log.** Every token write is logged as `api <site> [<token name>] <action> <ip>` (`journalctl -u file_server`).
- **Cache.** After a `replace`, a library file changes at once. A cloud file keeps its public URL, so Cloudflare and browsers can serve the old bytes until their cached copy expires; purge the URL in the Cloudflare dashboard if it matters. Page metadata changes at once.

## HTTP endpoints

Send `Authorization: Bearer <token>`. Request and response bodies are JSON. Errors come back as `{"error": "..."}`: 400 for a bad request, 401/429 for auth problems, 500 for a server failure.

| Method | Action | Body / query | Result |
|---|---|---|---|
| GET | `entry` | `?id=` | `{entry, path, children?, editions?}` |
| GET | `list` | `?folder=` (0 = root) | `{folder, path, children}` |
| GET | `search` | `?q=&max=` | `{entries}` (searches name, version, author and key; unpublished entries included) |
| GET | `source-url` | `?id=` | `{url}`: presigned GET, valid for 1 h |
| POST | `folder` | `{parentId, name}` | the new folder |
| POST | `upload-url` | `{fileName, parentId, name?, version?, contentType?}` | `{id, key, name, version, type, contentType, url}` |
| POST | `commit` | the `upload-url` result + `{parentId, size, author?, publishingYear?, printedYear?, description?, isPublished?}` | the new entry |
| POST | `replace-url` | `{id, fileName?}` (the extension must match) | `{id, key, contentType, url, backup}` |
| POST | `replace-commit` | `{id, numPages?, clearPreviews?}` | the entry (size read from the bucket) |
| POST | `save` | `{id, name?, version?, author?, publishingYear?, printedYear?, description?, isPublished?, parentId?, editionGroup?}` | the entry (a name, version or folder change moves the object) |
| POST | `publish` | `{id, isPublished}` | the entry |
| POST | `preview-urls` | `{id, count}` (1–6) | `{urls}`: PUT JPEGs with `x-amz-acl: public-read` |
| POST | `previews` | `{id, pages, numPages}` | the entry |
| POST | `editions` | `{ids}` or `{ids: [id], clear: true}` | `{editionGroup, ids}` |
| POST | `delete` | `{id}` | `{ok, backup}` (a folder must be empty) |

File bytes never pass through the server. `upload-url` and `replace-url` return a presigned PUT URL. Upload to it with exactly the `Content-Type` returned, then call `commit` or `replace-commit`. Uploading to a `replace-url` URL replaces the object in one step, so a failed upload leaves the old file in place.

With curl:

```bash
T=$(python3 -c "import json;print(json.load(open('config/secrets.json'))['apiTokens']['claude'])")
curl -s -H "Authorization: Bearer $T" "https://tipitaka.lk/library/admin/api/list?folder=1004"
```
