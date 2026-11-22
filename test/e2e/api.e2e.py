#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.10"
# dependencies = ["requests", "pymupdf", "boto3"]
# ///
"""End-to-end test of the token API (docs/api.md) through tools/fsapi.py, against the real bucket.

    npm run build && npm run test:e2e

Starts server/bin/file_server on :4321 with this repo's config/ and data/ if nothing is running there. The local
data/library.db must be a recent copy of the server's: preview images are keyed by entry id, so a stale database could
hand out an id that is in use and overwrite that entry's previews. Refresh it with
    ssh tipitaka.lk 'cd ~/www/file_server && ./file_server snapshot data/library.db /tmp/library.db' && scp tipitaka.lk:/tmp/library.db data/
Everything it makes is named "fs-v2 e2e ..." and is removed at the end, backups included, even after a failure.
"""
import json, os, subprocess, sys, tempfile, time
from pathlib import Path

import boto3, pymupdf, requests

ROOT = Path(__file__).resolve().parents[2]
BASE = "http://localhost:4321/library"
SECRETS = json.loads((ROOT / "config/secrets.json").read_text())
CFG = json.loads((ROOT / "config/library.json").read_text())
TAG = f"fs-v2 e2e {int(time.time())}"
steps = 0


def fsapi(*args):
    r = subprocess.run(["uv", "run", str(ROOT / "tools/fsapi.py"), *map(str, args)], capture_output=True, text=True,
                       env={**os.environ, "FS_API_URL": BASE})
    if r.returncode:
        raise AssertionError(f"fsapi {' '.join(map(str, args))}: {r.stderr.strip() or r.stdout.strip()}")
    return json.loads(r.stdout)


def add(made, *args):
    """fsapi add; the id goes into `made` at once, so a later failure still removes it. Returns the saved entry."""
    i = fsapi("add", *args)["entry"]["id"]
    made.insert(0, i)
    return fsapi("get", i)["entry"]


def remove_all(made):
    """rm in passes: a folder goes once the files and folders inside it are gone"""
    while made:
        left = []
        for i in made:
            try:
                fsapi("rm", i)
            except AssertionError as e:
                left.append((i, e))
        if len(left) == len(made):
            raise AssertionError("; ".join(str(e) for _, e in left))
        made[:] = [i for i, _ in left]


def check(what, ok, detail=""):
    global steps
    if not ok:
        raise AssertionError(f"{what} {detail}")
    steps += 1
    print("  ok  " + what)


def pdf(path, pages):
    doc = pymupdf.open()
    for i in range(pages):
        doc.new_page().insert_text((72, 100), f"{TAG} page {i + 1}", fontsize=20)
    doc.save(path)
    return path


def page(entry_id):
    return requests.get(f"{BASE}/{entry_id}", timeout=30)


def links(html, host):
    import re
    return [u.replace("&amp;", "&") for u in re.findall(rf'https://{re.escape(host)}/[^" ]+', html)]


def bucket_keys(s3):
    out = []
    for p in s3.get_paginator("list_objects_v2").paginate(Bucket=CFG["bucket"], Prefix=CFG["s3RootFolder"] + "/"):
        out += [o["Key"] for o in p.get("Contents", []) if "fs-v2 e2e" in o["Key"]]
    return out


def main():
    server = None
    try:
        requests.get(BASE + "/api/health", timeout=5)
    except requests.ConnectionError:
        server = subprocess.Popen([str(ROOT / "server/bin/file_server")], cwd=ROOT, env={**os.environ, "PORT": "4321", "LOG_REQUESTS": "0"})
        for _ in range(60):  # it loads both databases before listening
            try:
                requests.get(BASE + "/", timeout=5)
                break
            except requests.ConnectionError:
                time.sleep(0.5)
    s3 = boto3.client("s3", region_name="auto", endpoint_url="https://" + SECRETS["s3"]["endpoint"],
                      aws_access_key_id=SECRETS["s3"]["accessKeyId"], aws_secret_access_key=SECRETS["s3"]["secretAccessKey"])
    tmp = Path(tempfile.mkdtemp())
    made, preview_ids = [], []
    host = CFG["publicHost"]
    try:
        r = requests.get(BASE + "/admin/api/list?folder=0", timeout=10)
        check("no token: 401", r.status_code == 401)
        check("old admin pages: 404", requests.get(BASE + "/admin/login", timeout=10).status_code == 404)

        folder = fsapi("mkdir", 0, TAG)
        made.append(folder["id"])
        check("mkdir", folder["kind"] == "folder" and folder["name"] == TAG)

        book = add(made, pdf(tmp / "book.pdf", 3), "--parent", folder["id"], "--name", TAG + " book", "--author", "e2e",
                     "--publishing-year", 1999, "--description", "<p>hello <script>x</script></p>")
        preview_ids.append(book["id"])
        check("add pdf: page count and previews", book["numPages"] == 3 and len(book["previewPages"]) >= 1, json.dumps(book)[:300])
        check("description sanitised", "script" not in (book.get("description") or ""))

        html = page(book["id"])
        check("public page", html.status_code == 200 and TAG in html.text)
        signed = [u for u in links(html.text, host) if "Signature=" in u]
        check("signed link serves the file", signed and requests.get(signed[0], headers={"Range": "bytes=0-3"}, timeout=30).content == b"%PDF")
        check("signed link with fbclid", requests.get(signed[0] + "&fbclid=x", headers={"Range": "bytes=0-3"}, timeout=30).status_code == 206)
        prev = [u for u in links(html.text, host) if "/previews/" in u]
        check("preview image is public", prev and requests.get(prev[0], timeout=30).headers["content-type"] == "image/jpeg")

        old_key = book["s3Key"]
        book = fsapi("meta", book["id"], "--name", TAG + " renamed", "--version", "2nd ed")
        check("rename moves the object", book["s3Key"] != old_key and "renamed" in book["s3Key"] and "[2nd ed]" in book["s3Key"], book["s3Key"])
        signed = [u for u in links(page(book["id"]).text, host) if "Signature=" in u]
        check("link after rename", requests.get(signed[0], headers={"Range": "bytes=0-3"}, timeout=30).status_code == 206)

        rep = fsapi("replace", book["id"], pdf(tmp / "book2.pdf", 5))["entry"]
        check("replace: new page count", rep["numPages"] == 5, json.dumps(rep)[:300])
        check("replace made a backup", any("/backups/replaced/" in k for k in bucket_keys(s3)))

        (tmp / "note.txt").write_text(TAG)
        note = add(made, tmp / "note.txt", "--parent", folder["id"], "--name", TAG + " note", "--unpublished")
        check("add txt, unpublished", note["type"] == "txt" and not note["isPublished"] and page(note["id"]).status_code == 404)
        fsapi("meta", note["id"], "--published", "yes")
        check("publish", page(note["id"]).status_code == 200)

        fsapi("editions", book["id"], note["id"])
        check("editions shown on the page", f'/library/{note["id"]}"' in page(book["id"]).text)
        fsapi("editions", "--clear", note["id"])
        check("editions cleared", f'/library/{note["id"]}"' not in page(book["id"]).text)

        sub = fsapi("mkdir", folder["id"], TAG + " sub")
        made.insert(0, sub["id"])
        moved = fsapi("meta", note["id"], "--parent", sub["id"])
        check("move to another folder", moved["parentId"] == sub["id"] and " sub{" in moved["s3Key"], moved["s3Key"])

        check("search finds it", any(e["id"] == book["id"] for e in fsapi("search", TAG + " renamed")["entries"]))
        check("ls", {c["id"] for c in fsapi("ls", folder["id"], "--json")["children"]} == {book["id"], sub["id"]})
        (tmp / "dl").mkdir()
        got = fsapi("download", book["id"], tmp / "dl")
        check("download", any(p.read_bytes()[:4] == b"%PDF" for p in (tmp / "dl").rglob("*") if p.is_file()), str(got)[:200])

        s = fsapi("sync")
        check("sync dry run: bucket and database agree on the test entries",
              not any("fs-v2 e2e" in json.dumps(s[k]["first"], ensure_ascii=False) for k in ("added", "updated", "missing")), json.dumps(s, ensure_ascii=False)[:400])

        remove_all(made)
        check("rm: pages gone", page(book["id"]).status_code == 404 and page(folder["id"]).status_code == 404)
        check("rm backed the files up", sum("/backups/deleted/" in k for k in bucket_keys(s3)) == 2)
    finally:
        try:  # after a failure
            remove_all(made)
        except AssertionError as e:
            print("cleanup:", e)
        keys = bucket_keys(s3) + [f'{CFG["s3RootFolder"]}/previews/{i}-{n}.jpg' for i in preview_ids for n in range(6)]
        for k in keys:
            s3.delete_object(Bucket=CFG["bucket"], Key=k)
        left = bucket_keys(s3)
        if server:
            server.terminate()
        if left:
            sys.exit(f"left in the bucket: {left}")
    print(f"{steps} checks passed; nothing left in the bucket")


if __name__ == "__main__":
    main()
