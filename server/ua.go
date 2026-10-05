package main

// In-app browser (webview) detection for the "open in Chrome/Safari" helper (design §5.5).

import (
	"net/url"
	"regexp"
	"strings"
)

var inAppRe = regexp.MustCompile(`(?i)FBAN|FBAV|FB_IAB|FBIOS|FB4A|Instagram|Messenger|MessengerForiOS|Line/|MicroMessenger|Snapchat|TikTok|musical_ly|BytedanceWebview|Twitter for`)
var (
	androidRe = regexp.MustCompile(`(?i)Android`)
	iosRe     = regexp.MustCompile(`(?i)iPhone|iPad|iPod`)
)

type BrowserInfo struct {
	InApp   bool
	AppName string
	OS      string // android | ios | other
}

func detectBrowser(ua string) BrowserInfo {
	b := BrowserInfo{OS: "other"}
	if androidRe.MatchString(ua) {
		b.OS = "android"
	} else if iosRe.MatchString(ua) {
		b.OS = "ios"
	}
	m := inAppRe.FindString(ua)
	if m == "" {
		return b
	}
	b.InApp = true
	t := strings.ToLower(m)
	switch {
	case strings.HasPrefix(t, "insta"):
		b.AppName = "Instagram"
	case strings.HasPrefix(t, "messenger"):
		b.AppName = "Messenger"
	case strings.HasPrefix(t, "fb"):
		b.AppName = "Facebook"
	case strings.HasPrefix(t, "line"):
		b.AppName = "LINE"
	case strings.Contains(t, "micromessenger"):
		b.AppName = "WeChat"
	case strings.HasPrefix(t, "snap"):
		b.AppName = "Snapchat"
	case strings.HasPrefix(t, "twitter"):
		b.AppName = "X"
	default:
		b.AppName = "TikTok"
	}
	return b
}

// chromeIntent is an Android intent that opens u (our file page) in Chrome, falling back to the same URL.
func chromeIntent(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	search := ""
	if p.RawQuery != "" {
		search = "?" + p.RawQuery
	}
	return "intent://" + p.Host + p.EscapedPath() + search + "#Intent;scheme=" + p.Scheme + ";" +
		"package=com.android.chrome;S.browser_fallback_url=" + encodeURIComponent(u) + ";end"
}
