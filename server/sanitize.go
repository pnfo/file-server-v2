package main

// Allow-list for the description HTML written in the admin editor (rendered unescaped on public pages).
// Same rules as the Node server's sanitize.ts.

import (
	"regexp"
	"strings"
)

var allowedTags = map[string]bool{"p": true, "br": true, "b": true, "strong": true, "i": true, "em": true, "u": true,
	"ul": true, "ol": true, "li": true, "a": true, "h3": true, "h4": true, "blockquote": true}

var (
	commentRe  = regexp.MustCompile(`<!--[\s\S]*?-->`)
	tagRe      = regexp.MustCompile(`(?i)</?([a-z0-9]+)([^>]*)>`)
	hrefRe     = regexp.MustCompile(`(?i)href\s*=\s*("([^"]*)"|'([^']*)')`)
	httpRe     = regexp.MustCompile(`(?i)^https?://`)
	anyTagRe   = regexp.MustCompile(`<[^>]*>`)
	dropBlocks = []string{"script", "style", "iframe", "object", "embed", "template", "svg", "math"}
)

// stripBlocks removes <script ...>...</script> and the like, as /<(script|...)[\s\S]*?<\/\1\s*>/gi does.
func stripBlocks(s string) string {
	lower := asciiLower(s) // same byte offsets as s: only ASCII letters matter here
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '<' {
			if end := blockEnd(lower, i); end > 0 {
				i = end
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// blockEnd returns the end of a dropped block starting at s[i] == '<', or 0.
func blockEnd(lower string, i int) int {
	for _, name := range dropBlocks {
		if !strings.HasPrefix(lower[i+1:], name) {
			continue
		}
		for j := i + 1 + len(name); ; {
			k := strings.Index(lower[j:], "</"+name)
			if k < 0 {
				break
			}
			p := j + k + 2 + len(name)
			q := p
			for q < len(lower) && strings.ContainsRune(" \t\n\r\f\v", rune(lower[q])) {
				q++
			}
			if q < len(lower) && lower[q] == '>' {
				return q + 1
			}
			j = p
		}
	}
	return 0
}

func sanitizeHTML(html string) string {
	if html == "" {
		return ""
	}
	out := commentRe.ReplaceAllString(html, "")
	out = stripBlocks(out)
	out = tagRe.ReplaceAllStringFunc(out, func(tag string) string {
		m := tagRe.FindStringSubmatch(tag)
		n := strings.ToLower(m[1])
		if !allowedTags[n] {
			return ""
		}
		if strings.HasPrefix(tag, "</") {
			return "</" + n + ">"
		}
		if n == "a" {
			href := hrefRe.FindStringSubmatch(m[2])
			u := ""
			if href != nil {
				u = href[2]
				if u == "" {
					u = href[3]
				}
			}
			u = jsTrim(u)
			if httpRe.MatchString(u) {
				return `<a href="` + strings.ReplaceAll(u, `"`, "&quot;") + `" rel="nofollow noopener">`
			}
			return "<a>"
		}
		return "<" + n + ">"
	})
	out = jsTrim(out)
	if jsTrim(anyTagRe.ReplaceAllString(out, "")) == "" {
		return ""
	}
	return out
}
