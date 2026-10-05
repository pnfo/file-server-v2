package main

// SigV2 query-string auth for download links (design.md §5.1).
// Unlike SigV4, the signature covers only the path, the expiry and response-* overrides, so params
// that webviews append (fbclid, utm_*) do not invalidate the link. Verified against Spaces 2026-10-01.

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"regexp"
	"strconv"
	"strings"
)

const linkWindowSeconds = 6 * 3600

// windowEnd is the end of the next full window: links live 6-12 h, and everyone in a window gets an
// identical (CDN-cacheable) URL.
func windowEnd(nowMs int64) int64 { return (nowMs/1000/linkWindowSeconds + 2) * linkWindowSeconds }

// LinkMode: inline | attachment | stream (no Content-Disposition override, for <audio>)
type LinkMode string

type SignInput struct {
	ObjectKey       string // full key in the bucket, e.g. "library/අභිධර්ම{1}/x{2}.pdf"
	FileName        string // clean name the browser should save as
	Mode            LinkMode
	AccessKeyID     string
	SecretAccessKey string
	NowMs           int64
	// BaseURL is where the link points; default the Spaces CDN. The signature covers only the path, so any host
	// that forwards the path + query to Spaces works (e.g. the droplet's /fs-dl/ nginx proxy).
	BaseURL string
}

type SignedLink struct {
	URL     string `json:"url"`
	Expires int64  `json:"expires"`
}

func contentDisposition(mode LinkMode, fileName, asciiFallback string) string {
	return string(mode) + `; filename="` + asciiFallback + `"; filename*=UTF-8''` + encodeStrict(fileName)
}

var nonWordRe = regexp.MustCompile(`[^.\w]`)

func signLink(in SignInput) SignedLink {
	base := in.BaseURL
	if base == "" {
		base = "https://" + cdnHost
	}
	expires := windowEnd(in.NowMs)
	ext := ""
	if i := strings.LastIndex(in.FileName, "."); i >= 0 {
		ext = in.FileName[i:]
	}
	cd := ""
	if in.Mode != "stream" {
		cd = contentDisposition(in.Mode, in.FileName, "file"+nonWordRe.ReplaceAllString(ext, ""))
	}
	parts := strings.Split(in.ObjectKey, "/")
	for i, p := range parts {
		parts[i] = encodeStrict(p)
	}
	path := "/" + strings.Join(parts, "/")
	// response-* values go into the string-to-sign un-encoded
	sts := "GET\n\n\n" + strconv.FormatInt(expires, 10) + "\n/" + bucket + path
	if cd != "" {
		sts += "?response-content-disposition=" + cd
	}
	mac := hmac.New(sha1.New, []byte(in.SecretAccessKey))
	mac.Write([]byte(sts))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	u := base + path + "?AWSAccessKeyId=" + encodeStrict(in.AccessKeyID) + "&Expires=" + strconv.FormatInt(expires, 10) +
		"&Signature=" + encodeStrict(sig)
	if cd != "" {
		u += "&response-content-disposition=" + encodeStrict(cd)
	}
	return SignedLink{u, expires}
}
