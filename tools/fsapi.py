#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["requests", "pymupdf"]
# ///
"""fsapi - view, add and update files in the tipitaka.lk library/cloud through the admin API (docs/api.md).

    uv run tools/fsapi.py ls [FOLDER_ID]                 list a folder (0/omitted = root), unpublished included
    uv run tools/fsapi.py get ID                         one entry as JSON (with its path; children for folders)
    uv run tools/fsapi.py search TEXT [--max N]          name / version / author / key search
    uv run tools/fsapi.py download ID [OUT]              save the file
    uv run tools/fsapi.py add FILE --parent ID [--name --version --author --publishing-year --printed-year
                                                --description --unpublished] [--no-previews]
    uv run tools/fsapi.py replace ID FILE [--no-previews]  new content, same entry (the old object is backed up)
    uv run tools/fsapi.py meta ID [--name ... --parent ID --published yes|no --edition-group N]
    uv run tools/fsapi.py mkdir PARENT_ID NAME
    uv run tools/fsapi.py previews ID FILE [--pages 1,5]  (re)make PDF preview images from a local copy
    uv run tools/fsapi.py editions ID ID [...] | editions --clear ID
    uv run tools/fsapi.py rm ID                          delete (files are backed up first)
    uv run tools/fsapi.py sync [--run]                   compare the bucket with the database (dry run unless --run)

Settings: FS_API_URL (default https://tipitaka.lk/library; use .../cloud for the cloud site) and FS_API_TOKEN
(default: apiTokens.claude from config/secrets.json). Every command prints JSON.
"""

import argparse
import json
import mimetypes
import os
import sys
from pathlib import Path

import requests

ROOT = Path(__file__).resolve().parent.parent
BASE = os.environ.get("FS_API_URL", "https://tipitaka.lk/library").rstrip("/")
MAX_PREVIEWS = 6


def token() -> str:
    if t := os.environ.get("FS_API_TOKEN"):
        return t
    try:
        return json.loads((ROOT / "config/secrets.json").read_text())["apiTokens"]["claude"]
    except (OSError, KeyError, ValueError):
        sys.exit("set FS_API_TOKEN (or apiTokens.claude in config/secrets.json)")


S = requests.Session()
S.headers["Authorization"] = f"Bearer {token()}"


def api(action: str, method="POST", **body):
    url = f"{BASE}/admin/api/{action}"
    r = S.get(url, params=body, timeout=120) if method == "GET" else S.post(url, json=body, timeout=600)
    try:
        d = r.json()
    except ValueError:
        sys.exit(f"{action}: HTTP {r.status_code} {r.text[:300]}")
    if r.status_code != 200:
        sys.exit(f"{action}: HTTP {r.status_code} {d.get('error', d) if isinstance(d, dict) else d}")
    return d


def put(url: str, path: Path, content_type: str, extra=None):
    with open(path, "rb") as fh:
        r = requests.put(url, data=fh, headers={"Content-Type": content_type, **(extra or {})}, timeout=3600)
    if not r.ok:
        sys.exit(f"upload to bucket failed: HTTP {r.status_code} {r.text[:300]}")


def out(d):
    print(json.dumps(d, ensure_ascii=False, indent=2))


# ---- previews (same choice of pages and image size as the admin page / library-import) ----

def auto_pages(doc) -> list[int]:
    def blank(p):
        return not p.get_text().strip() and not p.get_images()

    n, pages = doc.page_count, []
    for start in (0, n // 2):
        i = start
        while i < n - 1 and (blank(doc[i]) or i in pages):
            i += 1
        if i not in pages:
            pages.append(i)
    return [i + 1 for i in pages]


def make_previews(entry_id: int, path: Path, pages: list[int] | None = None):
    import pymupdf

    doc = pymupdf.open(path)
    n = doc.page_count
    pages = [p for p in (pages or auto_pages(doc)) if 1 <= p <= n][:MAX_PREVIEWS] or [1]
    urls = api("preview-urls", id=entry_id, count=len(pages))["urls"]
    for url, p in zip(urls, pages):
        pg = doc[p - 1]
        z = 1500 / max(pg.rect.height, 1)
        jpg = pg.get_pixmap(matrix=pymupdf.Matrix(z, z)).tobytes("jpeg", jpg_quality=80)
        r = requests.put(url, data=jpg, headers={"Content-Type": "image/jpeg"}, timeout=120)
        r.raise_for_status()
    doc.close()
    return api("previews", id=entry_id, pages=pages, numPages=n)


def meta_body(a) -> dict:
    b = {}
    for k, key in (("name", "name"), ("version", "version"), ("author", "author"), ("description", "description"),
                   ("publishing_year", "publishingYear"), ("printed_year", "printedYear"), ("parent", "parentId"),
                   ("edition_group", "editionGroup")):
        v = getattr(a, k, None)
        if v is not None:
            b[key] = v
    if getattr(a, "published", None) is not None:
        b["isPublished"] = a.published == "yes"
    return b


# ---- commands ----

def cmd_ls(a):
    d = api("list", "GET", folder=a.folder)
    if a.json:
        return out(d)
    print("/" + "/".join(c["name"] for c in d["path"]))
    for e in d["children"]:
        flag = "" if e["isPublished"] else "  [unpublished]"
        ver = f" [{e['version']}]" if e.get("version") else ""
        size = "" if e["kind"] == "folder" else f"  {e['sizeBytes'] / 1e6:.1f} MB"
        print(f"{e['id']:>6}  {e['type']:<6} {e['name']}{ver}{size}{flag}")


def cmd_get(a):
    out(api("entry", "GET", id=a.id))


def cmd_search(a):
    out(api("search", "GET", q=a.text, max=a.max))


def cmd_download(a):
    e = api("entry", "GET", id=a.id)["entry"]
    url = api("source-url", "GET", id=a.id)["url"]
    dest = Path(a.out or e["s3Key"].rsplit("/", 1)[-1])
    if dest.is_dir():
        dest = dest / e["s3Key"].rsplit("/", 1)[-1]
    with requests.get(url, stream=True, timeout=3600) as r:
        r.raise_for_status()
        with open(dest, "wb") as fh:
            for chunk in r.iter_content(1 << 20):
                fh.write(chunk)
    out({"id": a.id, "saved": str(dest), "bytes": dest.stat().st_size})


def cmd_add(a):
    path = Path(a.file)
    start = {"fileName": path.name, "parentId": a.parent,
             "contentType": mimetypes.guess_type(path.name)[0] or ""}
    if a.name:
        start["name"] = a.name
    if a.version is not None:
        start["version"] = a.version
    up = api("upload-url", **start)
    put(up["url"], path, up["contentType"])
    commit = {**meta_body(a), "id": up["id"], "key": up["key"], "type": up["type"], "name": up["name"],
              "version": up["version"], "parentId": a.parent, "size": path.stat().st_size,
              "isPublished": not a.unpublished}
    e = api("commit", **commit)
    res = {"entry": e}
    if up["type"] == "pdf" and not a.no_previews:
        res["previews"] = make_previews(e["id"], path)["previewPages"]
    out(res)


def cmd_replace(a):
    path = Path(a.file)
    up = api("replace-url", id=a.id, fileName=path.name)
    put(up["url"], path, up["contentType"])
    pdf = path.suffix.lower() == ".pdf"
    e = api("replace-commit", id=a.id, clearPreviews=pdf and not a.no_previews)
    res = {"entry": e, "backup": up["backup"]}
    if pdf and not a.no_previews:
        res["entry"] = make_previews(a.id, path)
    out(res)


def cmd_meta(a):
    b = meta_body(a)
    if not b:
        sys.exit("nothing to change")
    out(api("save", id=a.id, **b))


def cmd_mkdir(a):
    out(api("folder", parentId=a.parent, name=a.name))


def cmd_previews(a):
    pages = [int(p) for p in a.pages.split(",")] if a.pages else None
    out(make_previews(a.id, Path(a.file), pages))


def cmd_editions(a):
    out(api("editions", ids=a.ids, clear=a.clear))


def cmd_rm(a):
    out(api("delete", id=a.id))


def cmd_sync(a):
    r = api("sync", dryRun=not a.run)
    for k in ("added", "updated", "restored", "missing", "skipped"):  # the lists can be long: counts, then the first few
        v = r.get(k) or []
        r[k] = {"count": len(v), "first": v[:20]}
    out(r)


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = p.add_subparsers(dest="cmd", required=True)

    def add_meta(sp, with_parent=False):
        sp.add_argument("--name")
        sp.add_argument("--version")
        sp.add_argument("--author")
        sp.add_argument("--description", help="HTML (sanitised by the server)")
        sp.add_argument("--publishing-year", type=int)
        sp.add_argument("--printed-year", type=int)
        if with_parent:
            sp.add_argument("--parent", type=int, help="move to this folder (0 = root)")
            sp.add_argument("--published", choices=["yes", "no"])
            sp.add_argument("--edition-group", type=int, help="0 removes it from its group")

    s = sub.add_parser("ls"); s.add_argument("folder", type=int, nargs="?", default=0); s.add_argument("--json", action="store_true"); s.set_defaults(f=cmd_ls)
    s = sub.add_parser("get"); s.add_argument("id", type=int); s.set_defaults(f=cmd_get)
    s = sub.add_parser("search"); s.add_argument("text"); s.add_argument("--max", type=int, default=100); s.set_defaults(f=cmd_search)
    s = sub.add_parser("download"); s.add_argument("id", type=int); s.add_argument("out", nargs="?"); s.set_defaults(f=cmd_download)
    s = sub.add_parser("add"); s.add_argument("file"); s.add_argument("--parent", type=int, required=True)
    add_meta(s); s.add_argument("--unpublished", action="store_true"); s.add_argument("--no-previews", action="store_true"); s.set_defaults(f=cmd_add)
    s = sub.add_parser("replace"); s.add_argument("id", type=int); s.add_argument("file"); s.add_argument("--no-previews", action="store_true"); s.set_defaults(f=cmd_replace)
    s = sub.add_parser("meta"); s.add_argument("id", type=int); add_meta(s, True); s.set_defaults(f=cmd_meta)
    s = sub.add_parser("mkdir"); s.add_argument("parent", type=int); s.add_argument("name"); s.set_defaults(f=cmd_mkdir)
    s = sub.add_parser("previews"); s.add_argument("id", type=int); s.add_argument("file"); s.add_argument("--pages", help="e.g. 1,12"); s.set_defaults(f=cmd_previews)
    s = sub.add_parser("editions"); s.add_argument("ids", type=int, nargs="+"); s.add_argument("--clear", action="store_true"); s.set_defaults(f=cmd_editions)
    s = sub.add_parser("rm"); s.add_argument("id", type=int); s.set_defaults(f=cmd_rm)
    s = sub.add_parser("sync"); s.add_argument("--run", action="store_true", help="apply (default: dry run)"); s.set_defaults(f=cmd_sync)

    a = p.parse_args()
    a.f(a)


if __name__ == "__main__":
    main()
