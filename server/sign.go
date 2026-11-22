package main

// Download links (design.md §5.1, moved from Spaces SigV2 to Cloudflare R2 in 2026-11).
//
// Signed sites (the library): the bucket is private and worker/src/index.ts, at the site's publicHost, checks
// the link before streaming the object. The signature covers only the path, the expiry and the
// Content-Disposition override, so params that webviews append (fbclid, utm_*) do not invalidate the link.
// Both sides are tested against the same vectors (testdata/js_vectors.json, "sign").
//
// Public sites (cloud): the bucket has a public custom domain and links are plain permanent URLs.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
)

const linkWindowSeconds = 6 * 3600

// windowEnd is the end of the next full window: links live 6-12 h, and everyone in a window gets an
// identical URL.
func windowEnd(nowMs int64) int64 { return (nowMs/1000/linkWindowSeconds + 2) * linkWindowSeconds }

// LinkMode: inline | attachment | stream (no Content-Disposition override, for <audio>)
type LinkMode string

type SignInput struct {
	Host      string // the Worker's hostname, e.g. library.tipitaka.lk
	ObjectKey string // full key in the bucket, e.g. "library/අභිධර්ම{1}/x{2}.pdf"
	FileName  string // clean name the browser should save as
	Mode      LinkMode
	Secret    string
	NowMs     int64
}

// SignedLink is a download link. Expires is 0 for a public link, which never expires.
type SignedLink struct {
	URL     string `json:"url"`
	Expires int64  `json:"expires"`
}

func contentDisposition(mode LinkMode, fileName, asciiFallback string) string {
	return string(mode) + `; filename="` + asciiFallback + `"; filename*=UTF-8''` + encodeStrict(fileName)
}

var nonWordRe = regexp.MustCompile(`[^.\w]`)

// keyPath is the URL path of an object key: every segment strictly percent-encoded. The Worker re-encodes the
// path it receives the same way before checking the signature, so a client that normalises escapes is fine.
func keyPath(objectKey string) string {
	parts := strings.Split(objectKey, "/")
	for i, p := range parts {
		parts[i] = encodeStrict(p)
	}
	return "/" + strings.Join(parts, "/")
}

// signLink: Signature = base64url(HMAC-SHA256(secret, "GET\n<expires>\n<path>[\n<content-disposition>]")).
func signLink(in SignInput) SignedLink {
	expires := windowEnd(in.NowMs)
	ext := ""
	if i := strings.LastIndex(in.FileName, "."); i >= 0 {
		ext = in.FileName[i:]
	}
	cd := ""
	if in.Mode != "stream" {
		cd = contentDisposition(in.Mode, in.FileName, "file"+nonWordRe.ReplaceAllString(ext, ""))
	}
	path := keyPath(in.ObjectKey)
	sts := "GET\n" + strconv.FormatInt(expires, 10) + "\n" + path
	if cd != "" {
		sts += "\n" + cd
	}
	mac := hmac.New(sha256.New, []byte(in.Secret))
	mac.Write([]byte(sts))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	u := "https://" + in.Host + path + "?Expires=" + strconv.FormatInt(expires, 10) + "&Signature=" + sig
	if cd != "" {
		u += "&response-content-disposition=" + encodeStrict(cd)
	}
	return SignedLink{u, expires}
}

// publicLink is the permanent URL of an object in a public bucket. A public bucket cannot rename the download
// per link, so the saved file keeps its key name; ?dl=1 asks for "Content-Disposition: attachment", which a
// response-header Transform Rule on the hostname adds (README, "Cloudflare").
func publicLink(host, objectKey string, mode LinkMode) SignedLink {
	u := "https://" + host + keyPath(objectKey)
	if mode == "attachment" {
		u += "?dl=1"
	}
	return SignedLink{URL: u}
}
