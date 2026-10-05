package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	logRequests        = os.Getenv("LOG_REQUESTS") != "0"
	legacyDownloadMode = os.Getenv("LEGACY_DOWNLOAD_MODE")
	startedAt          = time.Now()
)

// SiteCtx is the site a request came in on.
type SiteCtx struct {
	ID      string
	Base    string // URL prefix this request came in on, e.g. "/library" or "/library-v2"
	IsAlias bool
	Cfg     *SiteConfig
	Store   *SiteStore
	IsAdmin bool
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }

func newHandler() http.Handler {
	static := http.FileServer(http.Dir(distDir()))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		sw := &statusWriter{w, 200}
		segs := pathSegments(r)
		var prefix string
		if len(segs) > 0 {
			prefix = segs[0]
		}
		info, isSite := prefixes[prefix]
		switch {
		case isSite:
			s := &SiteCtx{ID: info.site, Base: "/" + prefix, IsAlias: info.isAlias, Cfg: getSiteConfig(info.site), Store: getStore(info.site)}
			if info.isAlias {
				w.Header().Set("X-Robots-Tag", "noindex")
			}
			serveSite(sw, r, s, segs[1:], static)
		case prefix == "fs-assets" || prefix == "fs-static": // nginx serves these in production
			static.ServeHTTP(sw, r)
		default:
			notFoundPage(sw)
		}
		rest := segs[min(1, len(segs)):]
		if logRequests && (len(rest) == 0 || rest[0] != "api") {
			log.Printf("%s %s %d %dms", r.Method, r.URL.Path, sw.status, time.Since(started).Milliseconds())
		}
	})
}

// pathSegments splits the escaped path and unescapes each segment (so %2F stays inside a search term).
func pathSegments(r *http.Request) []string {
	p := strings.TrimPrefix(r.URL.EscapedPath(), "/")
	if p == "" {
		return nil
	}
	parts := strings.Split(p, "/")
	for i, s := range parts {
		if u, err := url.PathUnescape(s); err == nil {
			parts[i] = u
		}
	}
	// a trailing slash matches the same route (Astro's trailingSlash: 'ignore')
	if len(parts) > 1 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

var idRe = regexp.MustCompile(`^\d+$`)

func parseID(s string) (int, bool) {
	if !idRe.MatchString(s) {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func serveSite(w http.ResponseWriter, r *http.Request, s *SiteCtx, rest []string, static http.Handler) {
	if len(rest) > 0 && rest[0] == "admin" {
		serveAdmin(w, r, s, rest[1:])
		return
	}
	get := r.Method == http.MethodGet || r.Method == http.MethodHead
	switch {
	case len(rest) == 0 || (len(rest) == 1 && rest[0] == ""):
		pageIndex(w, r, s)
	case rest[0] == "search" && len(rest) == 1 && get:
		q := strings.ToLower(jsTrim(r.URL.Query().Get("q")))
		if q != "" {
			redirect(w, s.Base+"/search/"+encodeURIComponent(q), http.StatusFound)
		} else {
			redirect(w, s.Base+"/", http.StatusFound)
		}
	case rest[0] == "search" && len(rest) == 2:
		pageSearch(w, r, s, rest[1])
	case rest[0] == "recent" && len(rest) == 1 && get:
		m, ok := jsParseInt(firstNonEmpty(r.URL.Query().Get("m"), "3"))
		if !ok || m == 0 {
			m = 3
		}
		redirect(w, fmt.Sprintf("%s/recent/%d", s.Base, m), http.StatusFound)
	case rest[0] == "recent" && len(rest) == 2:
		pageRecent(w, r, s, rest[1])
	case rest[0] == "api" && len(rest) == 2 && rest[1] == "search" && get:
		apiSearch(w, r, s)
	case rest[0] == "api" && len(rest) == 2 && rest[1] == "health" && get:
		apiHealth(w, s)
	case rest[0] == "api" && len(rest) == 3 && rest[1] == "hit" && r.Method == http.MethodPost:
		if id, ok := parseID(rest[2]); ok {
			s.Store.Hit(id, clientKey(r))
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	case rest[0] == "api" && len(rest) == 3 && rest[1] == "link" && get:
		apiLink(w, r, s, rest[2])
	case len(rest) == 1:
		if staticSiteFile(w, r, s, rest[0], static) {
			return
		}
		pageEntry(w, r, s, rest[0])
	case len(rest) == 2 && rest[1] == "download" && get:
		legacyDownload(w, r, s, rest[0])
	case len(rest) == 2 && rest[1] == "all" && get:
		// old-site links like /library/601/all (still requested by crawlers): send them to the entry page
		if id, ok := parseID(rest[0]); ok {
			redirect(w, fmt.Sprintf("%s/%d", s.Base, id), http.StatusMovedPermanently)
		} else {
			plainText(w, http.StatusNotFound, "Not found")
		}
	default:
		notFoundPage(w)
	}
}

// staticSiteFile serves public/<site>/ files (favicon, og image) under the site's own prefix (not aliases).
func staticSiteFile(w http.ResponseWriter, r *http.Request, s *SiteCtx, name string, static http.Handler) bool {
	if s.IsAlias || !strings.Contains(name, ".") || strings.Contains(name, "..") {
		return false
	}
	if _, err := os.Stat(filepath.Join(distDir(), s.ID, name)); err != nil {
		return false
	}
	static.ServeHTTP(w, r)
	return true
}

func redirect(w http.ResponseWriter, loc string, code int) {
	w.Header().Set("Location", loc)
	w.WriteHeader(code)
}

func clientKey(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		return strings.TrimSpace(strings.Split(f, ",")[0])
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return "unknown"
}

// plainText is a short text/plain response (http.Error appends a newline, the Node server didn't).
func plainText(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	w.Write([]byte(msg))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := marshalJSON(v)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(b)
}

func writeHTML(w http.ResponseWriter, status int, b []byte, err error) {
	if err != nil {
		log.Printf("render: %v", err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(b)
}

func notFoundPage(w http.ResponseWriter) {
	writeHTML(w, http.StatusNotFound, []byte(`<!DOCTYPE html><html lang="si"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>404 - Tipitaka.lk</title></head>`+
		`<body style="font-family:sans-serif;padding:2rem"><p>Not found. <a href="https://tipitaka.lk/library/">tipitaka.lk/library</a></p></body></html>`), nil)
}

// ---- public pages ----------------------------------------------------------------------------

// Page is what the public layout needs.
type Page struct {
	S            *SiteCtx
	Title        string // without " - Tipitaka.lk"
	FullTitle    string
	Description  string
	Canonical    string
	HasCanonical bool
	SearchQuery  string
	Assets       Assets
	D            any // page-specific data
}

func newPage(s *SiteCtx, title, description string) *Page {
	if title == "" {
		title = s.Cfg.RootFolderName
	}
	if description == "" {
		description = fmt.Sprintf("%s භාගත කරගන්න - tipitaka.lk බෞද්ධ %s.", s.Cfg.FileTypeName, s.Cfg.RootFolderName)
	}
	return &Page{S: s, Title: title, FullTitle: title + " - Tipitaka.lk", Description: description, Assets: assets}
}

func (p *Page) canonical(path string) *Page {
	p.Canonical, p.HasCanonical = p.S.Cfg.WebURLRoot+path, true
	return p
}

func pageIndex(w http.ResponseWriter, r *http.Request, s *SiteCtx) {
	st := s.Store
	st.mu.RLock()
	p := newPage(s, s.Cfg.RootFolderName+" මුල් පිටුව", "").canonical("")
	p.D = map[string]any{"Table": buildEntryTable(s, st.ChildrenOf(0), nil, "name", "asc"), "IsLibrary": s.ID == "library"}
	b, err := render("index", "layout", p)
	st.mu.RUnlock()
	w.Header().Set("Cache-Control", "no-cache")
	writeHTML(w, 200, b, err)
}

func pageEntry(w http.ResponseWriter, r *http.Request, s *SiteCtx, idStr string) {
	st := s.Store
	st.mu.RLock()
	var e *Entry
	if id, ok := parseID(idStr); ok {
		e = st.Get(id)
	}
	var p *Page
	status := 200
	switch {
	case e == nil:
		status = 404
		p = newPage(s, "", "")
	case e.IsFolder():
		p = newPage(s, e.Name, fmt.Sprintf("%s බහාලුමේ ඇති බෞද්ධ %s බාගත කරගන්න.", e.Name, s.Cfg.FileTypeName)).canonical(strconv.Itoa(e.ID))
		p.D = map[string]any{"Folder": e, "Nav": topNav(s, st.Parents(e.ID), e.Name, "", e.ID, true),
			"Table": buildEntryTable(s, st.ChildrenOf(e.ID), []string{"parent"}, "name", "asc")}
	default:
		p = newPage(s, e.Name, fmt.Sprintf("%s.%s %s බාගත කරගන්න.", e.Name, e.Type, s.Cfg.FileTypeName)).canonical(strconv.Itoa(e.ID))
		p.D = map[string]any{"File": buildFileView(s, e, r.UserAgent())}
	}
	b, err := render("entry", "layout", p)
	st.mu.RUnlock()
	// always revalidate: edits (renames, previews, publish) must show at once, and file pages embed signed links
	w.Header().Set("Cache-Control", "no-cache")
	writeHTML(w, status, b, err)
}

func pageRecent(w http.ResponseWriter, r *http.Request, s *SiteCtx, duration string) {
	// production semantics: the number is months
	months := 3
	if n, ok := jsParseInt(duration); ok && n > 0 && n <= 120 {
		months = n
	}
	options := []int{1, 2, 3, 4, 6, 9, 12}
	if !slices.Contains(options, months) {
		options = append(options, months)
		sort.Ints(options)
	}
	st := s.Store
	st.mu.RLock()
	p := newPage(s, fmt.Sprintf("පසුගිය මාස %d තුළ අලුතෙන් එකතු කළ ගොනු", months),
		fmt.Sprintf("Newly added recent files, books, recordings - tipitaka.lk බෞද්ධ %s.", s.Cfg.RootFolderName))
	p.D = map[string]any{"Months": months, "Options": options,
		"Table": buildEntryTable(s, st.Recent(months*30), []string{"parent"}, "dateAdded", "desc")}
	b, err := render("recent", "layout", p)
	st.mu.RUnlock()
	w.Header().Set("Cache-Control", "no-cache")
	writeHTML(w, 200, b, err)
}

// SearchView is the results block (also the live-search fragment).
type SearchView struct {
	Query, Icon, Color, Text, DataTitle string
	Table                               *EntryTable
}

func buildSearch(s *SiteCtx, query string) SearchView {
	res := s.Store.Search(query, searchMaxResults)
	v := SearchView{Query: query, DataTitle: fmt.Sprintf("“%s” සෙවුමේ ප්‍රතිඵල - Tipitaka.lk", query)}
	n, max := len(res.Entries), searchMaxResults
	switch {
	case query == "":
		v.Icon, v.Color, v.Text = "alert", "text-rose-500", "සෙවුම් පදයක් ඇතුළත් කර නොමැත."
	case res.TooManyTerms:
		v.Icon, v.Color, v.Text = "alert", "text-rose-500", fmt.Sprintf("“%s” යන සෙවුම සඳහා Singlish ප්‍රභේද වැඩි ප්‍රමාණයක් ඇත. කරුණාකර තවත් අකුරු එකතු කරන්න.", query)
	case n == 0:
		v.Icon, v.Color, v.Text = "alert", "text-rose-500", fmt.Sprintf("“%s” යන සෙවුම සඳහා ගැළපෙන ගොනු %sේ අඩංගු නොවේ.", query, s.Cfg.RootFolderName)
	case n < max:
		v.Icon, v.Color, v.Text = "check", "text-emerald-500", fmt.Sprintf("“%s” යන සෙවුම සඳහා ගැළපෙන ගොනු %d ක් හමුවිය.", query, n)
	default:
		v.Icon, v.Color, v.Text = "alert", "text-amber-500", fmt.Sprintf("ඔබගේ “%s” යන සෙවුම සඳහා ගැළපෙන ගොනු %d කට වඩා හමුවිය. එයින් මුල් %d පහත දැක්වේ.", query, max, max)
	}
	if n > 0 {
		v.Table = buildEntryTable(s, res.Entries, []string{"dateAdded"}, "name", "asc")
	}
	return v
}

func pageSearch(w http.ResponseWriter, r *http.Request, s *SiteCtx, q string) {
	query := sliceUTF16(jsTrim(q), 100)
	st := s.Store
	st.mu.RLock()
	p := newPage(s, fmt.Sprintf("“%s” සෙවුමේ ප්‍රතිඵල", query),
		fmt.Sprintf("“%s” යන සෙවුම සඳහා ගැළපෙන ගොනු - tipitaka.lk බෞද්ධ %s.", query, s.Cfg.RootFolderName))
	p.SearchQuery = query
	p.D = buildSearch(s, query)
	b, err := render("search", "layout", p)
	st.mu.RUnlock()
	w.Header().Set("Cache-Control", "no-cache")
	writeHTML(w, 200, b, err)
}

// apiSearch is the live-search fragment: just the results block, swapped in by client/site.ts.
func apiSearch(w http.ResponseWriter, r *http.Request, s *SiteCtx) {
	query := sliceUTF16(jsTrim(r.URL.Query().Get("q")), 100)
	st := s.Store
	st.mu.RLock()
	p := newPage(s, "", "")
	p.D = buildSearch(s, query)
	b, err := render("search", "searchResults", p)
	st.mu.RUnlock()
	w.Header().Set("Cache-Control", "no-cache")
	writeHTML(w, 200, b, err)
}

func apiLink(w http.ResponseWriter, r *http.Request, s *SiteCtx, idStr string) {
	st := s.Store
	st.mu.RLock()
	var e *Entry
	if id, ok := parseID(idStr); ok {
		e = st.Get(id)
	}
	if e == nil || !e.IsFile() {
		st.mu.RUnlock()
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	mode := LinkMode("inline")
	if m := r.URL.Query().Get("mode"); m == "attachment" || m == "stream" {
		mode = LinkMode(m)
	}
	link := st.Link(e, mode)
	st.mu.RUnlock()
	writeJSON(w, 200, link)
}

// legacyDownload: old /{id}/download links (shared on social media, embedded on other sites). Design §5.4:
// count the hit, then 302 straight to the signed CDN URL. No HTML bridge page - a 302 leaves no history entry,
// so Back works. LEGACY_DOWNLOAD_MODE=redirect-page sends browser navigations to the file page instead.
func legacyDownload(w http.ResponseWriter, r *http.Request, s *SiteCtx, idStr string) {
	st := s.Store
	st.mu.RLock()
	var e *Entry
	if id, ok := parseID(idStr); ok {
		e = st.Get(id)
	}
	if e == nil {
		st.mu.RUnlock()
		plainText(w, http.StatusNotFound, "Not found")
		return
	}
	id, isFolder := e.ID, e.IsFolder()
	var link SignedLink
	if !isFolder {
		link = st.Link(e, "inline")
	}
	st.mu.RUnlock()
	if isFolder {
		redirect(w, fmt.Sprintf("%s/%d", s.Base, id), http.StatusFound)
		return
	}
	if legacyDownloadMode == "redirect-page" && r.Header.Get("Sec-Fetch-Dest") == "document" {
		redirect(w, fmt.Sprintf("%s/%d", s.Base, id), http.StatusFound)
		return
	}
	st.Hit(id, clientKey(r))
	w.Header().Set("Cache-Control", "no-store")
	redirect(w, link.URL, http.StatusFound)
}

func apiHealth(w http.ResponseWriter, s *SiteCtx) {
	signer := checkSigner(s.Store)
	st := s.Store
	st.mu.RLock()
	entries := st.Size()
	st.mu.RUnlock()
	var lastSync any
	if v := st.DB.GetMeta("last_sync"); v != "" {
		lastSync = v
	}
	status := 200
	if !signer.OK {
		status = 503
	}
	writeJSON(w, status, orderedMap{{"ok", signer.OK}, {"site", s.ID}, {"entries", entries}, {"lastSync", lastSync},
		{"signer", signer}, {"rssMb", rssMB()}, {"uptimeS", int(time.Since(startedAt).Seconds() + 0.5)}, {"server", "go"}})
}

// ---- view models (the logic of the Astro components) ------------------------------------------

type TableHeader struct {
	Key, Label, Cls, AriaSort string
	Up, Down                  bool
}
type TableRow struct {
	E        *Entry
	Type     TypeInfo
	Size     SizeInfo
	Parent   *Entry
	DataPar  string
	NameFont string
}
type EntryTable struct {
	S                                        *SiteCtx
	Headers                                  []TableHeader
	Rows                                     []TableRow
	ShowParent, ShowDownloads, ShowDateAdded bool
	SortKey, SortDir, Status                 string
}

// buildEntryTable is the server-rendered file/folder table. Sorting is done in the browser
// by client/site.ts, which re-orders the rows using their data-* attributes. Expects s.Store.mu held.
func buildEntryTable(s *SiteCtx, entries []*Entry, exclude []string, sortKey, sortDir string) *EntryTable {
	hasFiles := false
	for _, e := range entries {
		hasFiles = hasFiles || e.IsFile()
	}
	// design-concerns.md rule 4: a folder-only list shows just name + size
	show := func(c string) bool { return hasFiles && !slices.Contains(exclude, c) }
	t := &EntryTable{S: s, ShowParent: show("parent"), ShowDownloads: show("downloads"), ShowDateAdded: show("dateAdded"), SortKey: sortKey, SortDir: sortDir}
	t.Headers = append(t.Headers, TableHeader{Key: "name", Label: s.Cfg.FileTypeName + " නම", Cls: "text-left"})
	if t.ShowParent {
		t.Headers = append(t.Headers, TableHeader{Key: "parent", Label: "බහාලුම", Cls: "hidden sm:table-cell text-left w-48"})
	}
	if t.ShowDownloads {
		t.Headers = append(t.Headers, TableHeader{Key: "downloads", Label: "බාගත ගණන", Cls: "hidden sm:table-cell text-right w-28"})
	}
	if t.ShowDateAdded {
		t.Headers = append(t.Headers, TableHeader{Key: "dateAdded", Label: "එක් කළ දිනය", Cls: "hidden sm:table-cell text-right w-32"})
	}
	t.Headers = append(t.Headers, TableHeader{Key: "size", Label: "ප්‍රමාණය", Cls: "text-right w-24 sm:w-28"})
	label := t.Headers[0].Label
	for i := range t.Headers {
		h := &t.Headers[i]
		if h.Key == sortKey {
			label = h.Label
			h.AriaSort = map[string]string{"asc": "ascending", "desc": "descending"}[sortDir]
			h.Up, h.Down = sortDir == "asc", sortDir == "desc"
		}
	}
	dir := "වැඩිවන"
	if sortDir == "desc" {
		dir = "අඩුවන"
	}
	t.Status = fmt.Sprintf("ඉහත ලැයිස්තුව “%s” %s පිළිවෙළට සැකසී ඇත.", label, dir)
	for _, e := range entries {
		row := TableRow{E: e, Type: getTypeInfo(e.Type), Size: getSizeInfo(e.SizeBytes), NameFont: fontClass(e.Name)}
		if t.ShowParent {
			row.Parent = s.Store.ParentOf(e)
			if row.Parent != nil {
				row.DataPar = row.Parent.Name
			}
		}
		t.Rows = append(t.Rows, row)
	}
	return t
}

func (h TableHeader) RightAligned() bool { return strings.Contains(h.Cls, "text-right") }

type NavView struct {
	S           *SiteCtx
	Parents     []*Entry
	Current     string
	CurrentIcon string
	HasCurrent  bool
	ShareID     int
	HasShare    bool
}

func topNav(s *SiteCtx, parents []*Entry, current, currentIcon string, shareID int, hasShare bool) NavView {
	return NavView{S: s, Parents: parents, Current: current, CurrentIcon: currentIcon, HasCurrent: true, ShareID: shareID, HasShare: hasShare}
}

type Stat struct{ K, V string }

// FileView is the only screen that starts a download (design §5.2). Every action is a plain link to a
// SigV2-signed CDN URL: no interstitial page and no redirect, so Back always returns here.
type FileView struct {
	S                        *SiteCtx
	E                        *Entry
	Nav                      NavView
	Type                     TypeInfo
	Size                     SizeInfo
	Audio, Viewable          bool
	Primary, Player          SignedLink
	Save                     *SignedLink
	Previews                 []Preview
	Browser                  BrowserInfo
	PageURL                  string
	ChromeIntent, SafariURL  template.URL
	Stats                    []Stat
	PrimaryText, PrimaryIcon string
	PrimaryMode, NameFont    string
	VersionFont              string
	Description              template.HTML
}

func buildFileView(s *SiteCtx, e *Entry, ua string) FileView {
	st := s.Store
	t := getTypeInfo(e.Type)
	v := FileView{S: s, E: e, Type: t, Size: getSizeInfo(e.SizeBytes), Audio: isAudio(e.Type), Viewable: isViewable(e.Type),
		Previews: st.Previews(e), Browser: detectBrowser(ua), PageURL: s.Cfg.WebURLRoot + strconv.Itoa(e.ID),
		NameFont: fontClass(e.Name + ".x"), VersionFont: fontClass(e.Version), Description: template.HTML(e.Description)}
	v.Nav = topNav(s, st.Parents(e.ID), "", t.Icon, 0, false)
	// audio: the player is for listening, so the button saves the file; everything else opens in the browser
	if v.Audio {
		v.Primary, v.PrimaryMode = st.Link(e, "attachment"), "attachment"
		v.Player = st.Link(e, "stream")
	} else {
		v.Primary, v.PrimaryMode = st.Link(e, "inline"), "inline"
		if v.Viewable {
			save := st.Link(e, "attachment")
			v.Save = &save
		}
	}
	v.ChromeIntent = template.URL(chromeIntent(v.PageURL))
	v.SafariURL = template.URL("x-safari-" + v.PageURL)
	if e.Author != "" {
		v.Stats = append(v.Stats, Stat{"කතෘ", e.Author})
	}
	if e.PublishingYear != 0 {
		v.Stats = append(v.Stats, Stat{"ප්‍රකාශිත වර්ෂය", strconv.Itoa(e.PublishingYear)})
	}
	if e.PrintedYear != 0 {
		v.Stats = append(v.Stats, Stat{"මුද්‍රිත වර්ෂය", strconv.Itoa(e.PrintedYear)})
	}
	if e.NumPages != 0 {
		v.Stats = append(v.Stats, Stat{"පිටු ගණන", formatNumber(e.NumPages)})
	}
	v.Stats = append(v.Stats, Stat{"බාගත කිරීම් ගණන", formatNumber(e.Downloads)}, Stat{"වෙබ් අඩවියට එක් කළ දිනය", e.DateAdded})
	if !v.Audio && v.Viewable {
		v.PrimaryText, v.PrimaryIcon = t.ViewText, "eye"
	} else {
		v.PrimaryText, v.PrimaryIcon = t.DownloadText, "download"
	}
	return v
}

// ---- admin -----------------------------------------------------------------------------------

func serveAdmin(w http.ResponseWriter, r *http.Request, s *SiteCtx, rest []string) {
	w.Header().Set("Cache-Control", "no-store")
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.IsAdmin = verifySession(c.Value)
	}
	isLogin := len(rest) > 0 && rest[0] == "login"
	isAPI := len(rest) > 0 && rest[0] == "api"
	if !s.IsAdmin && !isLogin {
		if isAPI {
			writeJSON(w, 401, map[string]string{"error": "login required"})
		} else {
			redirect(w, s.Base+"/admin/login", http.StatusFound)
		}
		return
	}
	// CSRF: admin API writes must come from our own scripts (a custom header can't be sent cross-site without CORS)
	if isAPI && r.Method != http.MethodGet && r.Header.Get("X-Fs-Admin") != "1" {
		plainText(w, http.StatusForbidden, "Forbidden")
		return
	}
	switch {
	case len(rest) == 0:
		adminIndex(w, r, s)
	case rest[0] == "login" && len(rest) == 1:
		adminLogin(w, r, s)
	case rest[0] == "logout" && len(rest) == 1 && r.Method == http.MethodPost:
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "deleted", Path: s.Base + "/admin", MaxAge: -1, Expires: time.Unix(0, 0)})
		redirect(w, s.Base+"/admin/login", http.StatusSeeOther)
	case rest[0] == "upload" && len(rest) == 1:
		adminUpload(w, r, s)
	case rest[0] == "bucket" && len(rest) == 1:
		adminBucket(w, r, s)
	case rest[0] == "edit" && len(rest) == 2:
		adminEdit(w, r, s, rest[1])
	case rest[0] == "api" && len(rest) == 2 && r.Method == http.MethodPost:
		adminAPI(w, r, s, rest[1])
	default:
		notFoundPage(w)
	}
}

// AdminPage is what the admin layout needs.
type AdminPage struct {
	S          *SiteCtx
	Title      string
	Assets     Assets
	Sites      []AdminSiteLink
	Nav        []AdminNavLink
	PublicHref string
	D          any
}
type AdminSiteLink struct {
	Href, Label string
	Active      bool
}
type AdminNavLink struct {
	Href, Label string
	Active      bool
}

func newAdminPage(r *http.Request, s *SiteCtx, title string) *AdminPage {
	p := &AdminPage{S: s, Title: title, Assets: assets}
	sub := strings.TrimPrefix(r.URL.Path, s.Base) // "/admin/..."
	q := jsTrim(r.URL.Query().Get("q"))
	folder := r.URL.Query().Get("folder")
	// "Public site" opens the public page for whatever is being looked at here
	switch m := regexp.MustCompile(`^/admin/edit/(\d+)`).FindStringSubmatch(sub); {
	case m != nil:
		p.PublicHref = s.Base + "/" + m[1]
	case q != "":
		p.PublicHref = s.Base + "/search/" + encodeURIComponent(strings.ToLower(q))
	case folder != "" && folder != "0":
		p.PublicHref = s.Base + "/" + folder
	default:
		p.PublicHref = s.Base + "/"
	}
	for _, id := range siteIDs {
		label := "Cloud"
		if id == "library" {
			label = "Library"
		}
		p.Sites = append(p.Sites, AdminSiteLink{"/" + prefixFor(id, s.IsAlias) + "/admin", label, id == s.ID})
	}
	upload := s.Base + "/admin/upload"
	if folder != "" {
		upload += "?folder=" + folder
	}
	p.Nav = []AdminNavLink{
		{s.Base + "/admin", "Files", sub == "/admin" || strings.HasPrefix(sub, "/admin/edit")},
		{upload, "Upload", strings.HasPrefix(sub, "/admin/upload")},
		{s.Base + "/admin/bucket", "Bucket", strings.HasPrefix(sub, "/admin/bucket")},
	}
	return p
}

type AdminRow struct {
	E         *Entry
	Type      TypeInfo
	Size      SizeInfo
	Href      string
	Muted     bool
	Downloads string
}

func adminIndex(w http.ResponseWriter, r *http.Request, s *SiteCtx) {
	st := s.Store
	q := strings.ToLower(jsTrim(r.URL.Query().Get("q")))
	folderID, _ := parseID(firstNonEmpty(r.URL.Query().Get("folder"), "0"))
	st.mu.RLock()
	defer st.mu.RUnlock()
	folder := st.GetAny(folderID)
	if folderID == 0 {
		folder = nil
	}
	// admin sees everything, including unpublished and missing entries.
	// search: id, plain substring (finds unpublished/missing too) and the public singlish search
	var rows []*Entry
	if q != "" {
		found := map[int]bool{}
		for _, e := range st.Search(q, 500).Entries {
			found[e.ID] = true
		}
		for _, e := range st.Ordered() {
			if found[e.ID] || strconv.Itoa(e.ID) == q || strings.Contains(strings.ToLower(e.Name), q) || strings.Contains(strings.ToLower(e.Version), q) {
				rows = append(rows, e)
			}
		}
	} else {
		for _, e := range st.Ordered() {
			if e.ParentID == folderID {
				rows = append(rows, e)
			}
		}
	}
	rows = rows[:min(len(rows), 500)]
	var crumbs []*Entry
	if folder != nil {
		crumbs = append(st.Parents(folder.ID), folder)
	}
	var view []AdminRow
	files := 0
	for _, e := range rows {
		href := fmt.Sprintf("%s/admin/edit/%d", s.Base, e.ID)
		if e.IsFolder() {
			href = fmt.Sprintf("%s/admin?folder=%d", s.Base, e.ID)
		} else {
			files++
		}
		dl := ""
		if e.IsFile() {
			dl = formatNumber(e.Downloads)
		}
		view = append(view, AdminRow{e, getTypeInfo(e.Type), getSizeInfo(e.SizeBytes), href, !(e.IsPublished && e.MissingSince == ""), dl})
	}
	title := "Files"
	if folder != nil && folder.Name != "" {
		title = folder.Name
	}
	heading := "All files"
	if q != "" {
		heading = "Search: " + q
	} else if folder != nil {
		heading = folder.Name
	}
	p := newAdminPage(r, s, title)
	p.D = map[string]any{"Q": q, "FolderID": folderID, "Folder": folder, "Crumbs": crumbs, "Rows": view, "Heading": heading,
		"FolderCount": len(rows) - files, "FileCount": files, "Capped": len(rows) == 500}
	b, err := render("admin_index", "adminLayout", p)
	writeHTML(w, 200, b, err)
}

func adminLogin(w http.ResponseWriter, r *http.Request, s *SiteCtx) {
	errMsg := ""
	if r.Method == http.MethodPost {
		ip := clientKey(r)
		if !loginAllowed(ip) {
			errMsg = "Too many attempts. Try again in 15 minutes."
		} else {
			ok := checkPassword(r.FormValue("password"))
			recordLogin(ip, ok)
			if ok {
				http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: createSession(), Path: s.Base + "/admin", MaxAge: sessionTTL,
					HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.Header.Get("X-Forwarded-Proto") == "https"})
				redirect(w, s.Base+"/admin", http.StatusFound)
				return
			}
			errMsg = "Incorrect password."
		}
	}
	p := newAdminPage(r, s, "Sign in")
	p.D = map[string]any{"Error": errMsg}
	b, err := render("admin_login", "adminLayout", p)
	writeHTML(w, 200, b, err)
}

func adminUpload(w http.ResponseWriter, r *http.Request, s *SiteCtx) {
	st := s.Store
	folderID, _ := parseID(firstNonEmpty(r.URL.Query().Get("folder"), "0"))
	type opt struct {
		ID   int
		Path string
	}
	st.mu.RLock()
	var folders []opt
	for _, f := range st.byIDOrder() {
		if !f.IsFolder() {
			continue
		}
		var names []string
		for _, p := range append(st.Parents(f.ID), f) {
			names = append(names, p.Name)
		}
		folders = append(folders, opt{f.ID, strings.Join(names, " / ")})
	}
	st.mu.RUnlock()
	coll := newCollator()
	sort.SliceStable(folders, func(i, j int) bool { return coll.Compare(folders[i].Path, folders[j].Path) < 0 })
	p := newAdminPage(r, s, "Upload")
	p.D = map[string]any{"FolderID": folderID, "Folders": folders}
	b, err := render("admin_upload", "adminLayout", p)
	writeHTML(w, 200, b, err)
}

func adminBucket(w http.ResponseWriter, r *http.Request, s *SiteCtx) {
	lastSync := s.Store.DB.GetMeta("last_sync")
	if lastSync != "" {
		lastSync = strings.Replace(lastSync, "T", " ", 1)[:min(16, len(lastSync))] + " UTC"
	} else {
		lastSync = "never"
	}
	p := newAdminPage(r, s, "Bucket")
	p.D = map[string]any{"LastSync": lastSync}
	b, err := render("admin_bucket", "adminLayout", p)
	writeHTML(w, 200, b, err)
}

func adminEdit(w http.ResponseWriter, r *http.Request, s *SiteCtx, idStr string) {
	st := s.Store
	st.mu.RLock()
	defer st.mu.RUnlock()
	var e *Entry
	if id, ok := parseID(idStr); ok {
		e = st.GetAny(id)
	}
	if e == nil {
		redirect(w, s.Base+"/admin", http.StatusFound)
		return
	}
	pages := make([]string, len(e.PreviewPages))
	for i, n := range e.PreviewPages {
		pages[i] = strconv.Itoa(n)
	}
	p := newAdminPage(r, s, "Edit "+e.Name)
	p.D = map[string]any{"E": e, "Type": getTypeInfo(e.Type), "Size": getSizeInfo(e.SizeBytes), "Previews": st.Previews(e),
		"Crumbs": st.Parents(e.ID), "HasChildren": e.IsFolder() && st.anyChild(e.ID), "IsFile": e.IsFile(),
		"PagesValue": strings.Join(pages, ", "), "ParentForDelete": max(e.ParentID, 0), "Description": template.HTML(e.Description)}
	b, err := render("admin_edit", "adminLayout", p)
	writeHTML(w, 200, b, err)
}

func adminAPI(w http.ResponseWriter, r *http.Request, s *SiteCtx, action string) {
	st := s.Store
	b := body{}
	json.NewDecoder(r.Body).Decode(&b)
	if b == nil {
		b = body{}
	}
	id := orZeroNaN(b.num("id"))
	var res any
	var err error
	switch action {
	case "save":
		res, err = st.SaveMeta(id, b)
	case "publish":
		res, err = st.SaveMeta(id, body{"isPublished": b.truthy("isPublished")})
	case "delete":
		if err = st.DeleteEntry(id); err == nil {
			res = map[string]bool{"ok": true}
		}
	case "folder":
		res, err = st.CreateFolder(b.intOr0("parentId"), b["name"])
	case "upload-url":
		res, err = st.StartUpload(b)
	case "commit":
		res, err = st.CommitUpload(b)
	case "preview-urls":
		var urls []string
		if urls, err = st.PreviewUploadURLs(id, b.num("count")); err == nil {
			res = map[string][]string{"urls": urls}
		}
	case "previews":
		pages, _ := b["pages"].([]any)
		res, err = st.SavePreviews(id, pages, b.intOr0("numPages"))
	case "source-url":
		var u string
		if u, err = st.SourceURL(id); err == nil {
			res = map[string]string{"url": u}
		}
	case "sync":
		var counts CountInfo
		if b.truthy("withCounts") && s.Cfg.IDToInfoFile != "" {
			counts, err = readCountsFile(s.Cfg.IDToInfoFile)
		}
		if err == nil {
			res, err = syncFromS3(st, b.truthy("dryRun"), counts)
		}
	case "cors-get":
		res = map[string]any{"rules": s3GetCors()}
	case "cors-set":
		// add to the existing origins: setting it from prod must not drop http://localhost:4321 (dev, e2e) or vice versa
		var origins []string
		for _, rule := range s3GetCors() {
			origins = append(origins, rule.AllowedOrigins...)
		}
		origins = append(origins, "https://tipitaka.lk", requestOrigin(r))
		if extra, ok := b["origins"].([]any); ok {
			for _, o := range extra {
				if s, ok := o.(string); ok {
					origins = append(origins, s)
				}
			}
		}
		valid := regexp.MustCompile(`^https?://[^/]+$`)
		var keep []string
		for _, o := range origins {
			if valid.MatchString(o) && !slices.Contains(keep, o) {
				keep = append(keep, o)
			}
		}
		if err = s3PutCors(keep); err == nil {
			res = map[string]any{"rules": s3GetCors()}
		}
	default:
		writeJSON(w, 404, map[string]string{"error": "unknown action " + action})
		return
	}
	if err != nil {
		var ae *AdminError
		if errors.As(err, &ae) {
			writeJSON(w, 400, map[string]string{"error": ae.msg})
		} else {
			log.Printf("admin %s failed: %v", action, err)
			writeJSON(w, 500, map[string]string{"error": err.Error()})
		}
		return
	}
	writeJSON(w, 200, res)
}

// orZeroNaN is Number(b.id) as an id: NaN (and fractions) become nanID, which matches no entry.
func orZeroNaN(f float64) int {
	if f != f || f != float64(int(f)) {
		return nanID
	}
	return int(f)
}

func requestOrigin(r *http.Request) string {
	scheme := "http"
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	}
	return scheme + "://" + r.Host
}
