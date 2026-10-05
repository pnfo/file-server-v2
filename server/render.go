package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed templates/*.html
var templateFS embed.FS

// Assets are the hashed file names written by scripts/build-assets.mjs (dist/manifest.json).
type Assets struct {
	SiteJS   string `json:"site.js"`
	AdminJS  string `json:"admin.js"`
	SiteCSS  string `json:"site.css"`
	AdminCSS string `json:"admin.css"`
}

var assets Assets

func loadAssets() {
	b, err := os.ReadFile(filepath.Join(distDir(), "manifest.json"))
	if err != nil {
		log.Printf("no asset manifest (%v) - run: node scripts/build-assets.mjs", err)
		return
	}
	if err := json.Unmarshal(b, &assets); err != nil {
		log.Fatalf("manifest.json: %v", err)
	}
}

// compressHTML removes template indentation the way Astro's compressHTML does: whitespace containing a newline
// disappears next to a tag or a template action, and becomes one space between two pieces of text.
var wsRe = regexp.MustCompile(`[ \t\r\n]*\n[ \t\r\n]*`)

func compressHTML(src string) string {
	var b strings.Builder
	last := 0
	for _, m := range wsRe.FindAllStringIndex(src, -1) {
		before, after := src[last:m[0]], src[m[1]:]
		b.WriteString(before)
		prev := strings.TrimRight(src[:m[0]], " \t")
		atTag := strings.HasSuffix(prev, ">") || strings.HasSuffix(prev, "}}") || prev == "" ||
			strings.HasPrefix(after, "<") || strings.HasPrefix(after, "{{") || after == ""
		if !atTag {
			b.WriteByte(' ')
		}
		last = m[1]
	}
	b.WriteString(src[last:])
	return b.String()
}

var funcs = template.FuncMap{
	"icon":        icon,
	"iconTitle":   iconTitle,
	"fontClass":   fontClass,
	"size":        getSizeInfo,
	"typeInfo":    getTypeInfo,
	"unitColor":   sizeUnitColor,
	"num":         formatNumber,
	"trustURL":    func(s string) template.URL { return template.URL(s) },
	"trustHTML":   func(s string) template.HTML { return template.HTML(s) },
	"upper":       strings.ToUpper,
	"slice10":     func(s string) string { return s[:min(10, len(s))] },
	"join":        strings.Join,
	"dict":        dict,
	"themeScript": func() template.HTML { return themeScript },
	"adminThemeScript": func() template.HTML {
		return `<script>try { document.documentElement.classList.toggle('dark', localStorage.getItem('fs-theme') !== 'light') } catch {}</script>`
	},
	"sprite": func() template.HTML { return iconSprite },
	// hiddenUnless is `${cond ? '' : 'hidden'}`
	"hiddenUnless": func(b bool) string {
		if b {
			return ""
		}
		return "hidden"
	},
}

const themeScript template.HTML = `<script>
    // theme: our key, else the old site's saved setting (same origin), else dark
    try {
      let t = localStorage.getItem('fs-theme')
      if (!t) { const o = JSON.parse(localStorage.getItem('settings') || '{}'); t = o.darkMode === false ? 'light' : 'dark' }
      document.documentElement.classList.toggle('dark', t !== 'light')
    } catch {}
  </script>`

// icon renders <Icon name class />: a reference into the page's sprite.
func icon(name string, class ...string) template.HTML {
	if !iconNames[name] {
		log.Printf("unknown icon %q", name)
	}
	cls := ""
	if len(class) > 0 {
		cls = class[0]
	}
	return template.HTML(`<svg class="icon ` + template.HTMLEscapeString(cls) + `" aria-hidden="true"><use href="#i-` + name + `"></use></svg>`)
}

func iconTitle(name, class, title string) template.HTML {
	return template.HTML(`<svg class="icon ` + template.HTMLEscapeString(class) + `" role="img"><title>` +
		template.HTMLEscapeString(title) + `</title><use href="#i-` + name + `"></use></svg>`)
}

func dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, fmt.Errorf("dict: odd number of arguments")
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m, nil
}

// pages maps a page template file to its parsed set (the shared components plus that page).
var pages = map[string]*template.Template{}

func loadTemplates() {
	read := func(name string) string {
		b, err := templateFS.ReadFile("templates/" + name)
		if err != nil {
			log.Fatal(err)
		}
		return compressHTML(string(b))
	}
	base := template.New("base").Funcs(funcs)
	for _, shared := range []string{"layout.html", "admin_layout.html", "components.html"} {
		template.Must(base.New(shared).Parse(read(shared)))
	}
	entries, err := fs.ReadDir(templateFS, "templates")
	if err != nil {
		log.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "page_") {
			continue
		}
		t := template.Must(base.Clone())
		template.Must(t.New(name).Parse(read(name)))
		pages[strings.TrimSuffix(strings.TrimPrefix(name, "page_"), ".html")] = t
	}
}

// render executes a page template ("index" for page_index.html) or a named template in it.
func render(page, name string, data any) ([]byte, error) {
	t := pages[page]
	if t == nil {
		return nil, fmt.Errorf("no template %s", page)
	}
	var b bytes.Buffer
	if err := t.ExecuteTemplate(&b, name, data); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
