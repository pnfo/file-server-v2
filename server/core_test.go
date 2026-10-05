package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// testdata/js_vectors.json and collation_si.txt hold outputs of the Node server's code
// (captured by scripts/gen-go-fixtures.ts before the Node code was removed; see git history), so these tests
// check the Go port against the original.
func loadVectors(t *testing.T) map[string]json.RawMessage {
	b, err := os.ReadFile("testdata/js_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]json.RawMessage
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func pairs(t *testing.T, raw json.RawMessage) [][2]json.RawMessage {
	var out [][2]json.RawMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSizeInfoMatchesJS(t *testing.T) {
	for _, p := range pairs(t, loadVectors(t)["sizes"]) {
		var n float64
		var want struct{ Numeral, Unit string }
		json.Unmarshal(p[0], &n)
		json.Unmarshal(p[1], &want)
		if got := getSizeInfo(int64(n)); got.Numeral != want.Numeral || got.Unit != want.Unit {
			t.Errorf("getSizeInfo(%v) = %v, want %v", n, got, want)
		}
	}
}

func TestFormattingMatchesJS(t *testing.T) {
	v := loadVectors(t)
	for _, p := range pairs(t, v["numbers"]) {
		var n int
		var want string
		json.Unmarshal(p[0], &n)
		json.Unmarshal(p[1], &want)
		if got := formatNumber(n); got != want {
			t.Errorf("formatNumber(%d) = %q, want %q", n, got, want)
		}
	}
	for _, p := range pairs(t, v["fontClass"]) {
		var in, want string
		json.Unmarshal(p[0], &in)
		json.Unmarshal(p[1], &want)
		if got := fontClass(in); got != want {
			t.Errorf("fontClass(%q) = %q, want %q", in, got, want)
		}
	}
	var types [][3]string
	json.Unmarshal(v["types"], &types)
	for _, c := range types {
		if got := getTypeInfo(c[0]); got.Icon != c[1] || got.Color != c[2] {
			t.Errorf("getTypeInfo(%q) = %s %s, want %s %s", c[0], got.Icon, got.Color, c[1], c[2])
		}
	}
	for _, p := range pairs(t, v["encode"]) {
		var in, want string
		json.Unmarshal(p[0], &in)
		json.Unmarshal(p[1], &want)
		if got := encodeStrict(in); got != want {
			t.Errorf("encodeStrict(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeMatchesJS(t *testing.T) {
	for _, p := range pairs(t, loadVectors(t)["sanitize"]) {
		var in string
		var want *string
		json.Unmarshal(p[0], &in)
		json.Unmarshal(p[1], &want)
		w := ""
		if want != nil {
			w = *want
		}
		if got := sanitizeHTML(in); got != w {
			t.Errorf("sanitizeHTML(%q) = %q, want %q", in, got, w)
		}
	}
}

func TestNamingMatchesJS(t *testing.T) {
	v := loadVectors(t)
	for _, p := range pairs(t, v["parse"]) {
		var in string
		var want struct {
			Name    string
			Version *string
			ID      int
			Type    *string
		}
		json.Unmarshal(p[0], &in)
		json.Unmarshal(p[1], &want)
		got := parseFileName(in)
		wv, wt := "", ""
		if want.Version != nil {
			wv = *want.Version
		}
		if want.Type != nil {
			wt = *want.Type
		}
		if got.Name != want.Name || got.Version != wv || got.ID != want.ID || got.Type != wt {
			t.Errorf("parseFileName(%q) = %+v, want %+v", in, got, want)
		}
	}
	for _, p := range pairs(t, v["build"]) {
		var args []any
		var want string
		json.Unmarshal(p[0], &args)
		json.Unmarshal(p[1], &want)
		version, _ := args[1].(string)
		if got := buildFileName(args[0].(string), version, int(args[2].(float64)), args[3].(string)); got != want {
			t.Errorf("buildFileName(%v) = %q, want %q", args, got, want)
		}
	}
	for _, p := range pairs(t, v["saveAs"]) {
		var e struct {
			Name    string
			Version *string
			Type    string
		}
		var want string
		json.Unmarshal(p[0], &e)
		json.Unmarshal(p[1], &want)
		ent := &Entry{Name: e.Name, Type: e.Type}
		if e.Version != nil {
			ent.Version = *e.Version
		}
		if got := saveAsName(ent); got != want {
			t.Errorf("saveAsName(%+v) = %q, want %q", e, got, want)
		}
	}
}

func TestSignLinkMatchesJS(t *testing.T) {
	var signs []struct {
		ObjectKey, FileName, Mode, BaseURL string
		Out                                SignedLink
	}
	json.Unmarshal(loadVectors(t)["sign"], &signs)
	now := time.Date(2026, 10, 1, 7, 30, 0, 0, time.UTC).UnixMilli()
	for _, s := range signs {
		got := signLink(SignInput{ObjectKey: s.ObjectKey, FileName: s.FileName, Mode: LinkMode(s.Mode), AccessKeyID: "AK/+=",
			SecretAccessKey: "SECRET", NowMs: now, BaseURL: s.BaseURL})
		if got != s.Out {
			t.Errorf("signLink(%s)\n got %+v\nwant %+v", s.ObjectKey, got, s.Out)
		}
	}
}

func TestSignLinkSignature(t *testing.T) {
	nowMs := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	l := signLink(SignInput{ObjectKey: "library/අ{1}/x[v]{2}.pdf", FileName: "x - v.pdf", Mode: "inline", AccessKeyID: "AK", SecretAccessKey: "SECRET", NowMs: nowMs})
	u, err := url.Parse(l.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != cdnHost {
		t.Errorf("host %s", u.Host)
	}
	if p, _ := url.PathUnescape(u.EscapedPath()); p != "/library/අ{1}/x[v]{2}.pdf" {
		t.Errorf("path %s", p)
	}
	cd := u.Query().Get("response-content-disposition")
	if cd != `inline; filename="file.pdf"; filename*=UTF-8''x%20-%20v.pdf` {
		t.Errorf("disposition %s", cd)
	}
	sts := "GET\n\n\n" + u.Query().Get("Expires") + "\n/tipitaka" + u.EscapedPath() + "?response-content-disposition=" + cd
	m := hmac.New(sha1.New, []byte("SECRET"))
	m.Write([]byte(sts))
	if u.Query().Get("Signature") != base64.StdEncoding.EncodeToString(m.Sum(nil)) {
		t.Error("signature mismatch")
	}
}

func TestLinkWindows(t *testing.T) {
	ts := time.Date(2026, 10, 1, 7, 30, 0, 0, time.UTC).UnixMilli()
	e := windowEnd(ts)
	if e != time.Date(2026, 10, 1, 18, 0, 0, 0, time.UTC).Unix() {
		t.Errorf("windowEnd = %d", e)
	}
	if life := e - ts/1000; life < linkWindowSeconds || life > 2*linkWindowSeconds {
		t.Errorf("lifetime %d", life)
	}
	if windowEnd(ts+3600_000) != e {
		t.Error("same window, different expiry")
	}
}

func TestBrowserDetectionMatchesJS(t *testing.T) {
	v := loadVectors(t)
	for _, p := range pairs(t, v["ua"]) {
		var ua string
		var want struct {
			InApp   bool
			AppName *string
			OS      string
		}
		json.Unmarshal(p[0], &ua)
		json.Unmarshal(p[1], &want)
		got := detectBrowser(ua)
		wn := ""
		if want.AppName != nil {
			wn = *want.AppName
		}
		if got.InApp != want.InApp || got.AppName != wn || got.OS != want.OS {
			t.Errorf("detectBrowser(%q) = %+v, want %+v", ua, got, want)
		}
	}
	for _, p := range pairs(t, v["intent"]) {
		var in, want string
		json.Unmarshal(p[0], &in)
		json.Unmarshal(p[1], &want)
		if got := chromeIntent(in); got != want {
			t.Errorf("chromeIntent(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCollationMatchesICU checks every adjacent pair of the fixture (sorted by Intl.Collator('si') in Node, ties
// marked "=") with the Go collator: same order, and equal exactly where ICU says equal.
func TestCollationMatchesICU(t *testing.T) {
	b, err := os.ReadFile("testdata/collation_si.txt")
	if err != nil {
		t.Fatal(err)
	}
	c := newCollator()
	var prev []byte
	bad := 0
	for i, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		rel, s, _ := strings.Cut(line, "\t")
		key := c.Key(s)
		if i > 0 {
			want := -1
			if rel == "=" {
				want = 0
			}
			if got := bytes.Compare(prev, key); got != want {
				if bad < 10 {
					t.Errorf("line %d %+q: compare with previous = %d, want %d", i+1, s, got, want)
				}
				bad++
			}
		}
		prev = key
	}
	if bad > 0 {
		t.Errorf("%d pairs differ", bad)
	}
	if c.Compare("කං", "කක") >= 0 || c.Compare("ඖ", "කං") >= 0 {
		t.Error("anusvara sorts after the vowels and before ක")
	}
}

func TestEntriesFromListing(t *testing.T) {
	o := func(key string) S3Object { return S3Object{Key: "library/" + key, Size: 10} }
	files, _, folders, _, skipped := entriesFromListing([]S3Object{
		o("අභිධර්ම{1}/පොත[1965]{2}.pdf"), o("අභිධර්ම{1}/sub{3}/b{4}.mp3"), o("thumbs/2-0.jpg"),
		o("-dev/test{7}/c{8}.pdf"), o("dup{2}.pdf"), o("noid.pdf"),
	}, "library")
	var fids []int
	for id := range folders {
		fids = append(fids, id)
	}
	sort.Ints(fids)
	if len(fids) != 3 || fids[0] != 1 || fids[1] != 3 || fids[2] != 7 {
		t.Errorf("folders %v", fids)
	}
	if folders[7].parentID != -1 || files[2].version != "1965" || files[4].parentID != 3 || files[8].parentID != 7 {
		t.Errorf("files %+v folders %+v", files, folders)
	}
	if len(skipped) != 2 || skipped[0].Key != "dup{2}.pdf" || skipped[1].Key != "noid.pdf" {
		t.Errorf("skipped %+v", skipped)
	}
}

func TestCompressHTML(t *testing.T) {
	cases := [][2]string{
		{"<p>\n  text\n</p>", "<p>text</p>"},
		{"one\n  two", "one two"},
		{"සහ\n  {{icon}}", "සහ{{icon}}"},
		{"{{end}}\n  <div>", "{{end}}<div>"},
		{"a <b>c</b> d", "a <b>c</b> d"},
		{`<a x="1"` + "\n    " + `y="2">`, `<a x="1" y="2">`},
	}
	for _, c := range cases {
		if got := compressHTML(c[0]); got != c[1] {
			t.Errorf("compressHTML(%q) = %q, want %q", c[0], got, c[1])
		}
	}
}

func TestJSHelpers(t *testing.T) {
	for in, want := range map[string]float64{"": 0, " 12 ": 12, "1990.0": 1990, "0x10": 16, "1e3": 1000} {
		if got := jsNumber(in, true); got != want {
			t.Errorf("jsNumber(%q) = %v", in, got)
		}
	}
	for _, in := range []any{"abc", "1_000", "NaN", []any{1}} {
		if got := jsNumber(in, true); got == got {
			t.Errorf("jsNumber(%v) = %v, want NaN", in, got)
		}
	}
	if got := jsNumber(nil, false); got == got {
		t.Error("missing should be NaN")
	}
	for in, want := range map[string]int{"3": 3, " 12x": 12, "-5": -5, "+7": 7} {
		if got, ok := jsParseInt(in); !ok || got != want {
			t.Errorf("jsParseInt(%q) = %d %v", in, got, ok)
		}
	}
	if _, ok := jsParseInt("x1"); ok {
		t.Error("jsParseInt(x1) should be NaN")
	}
	if got := sliceUTF16("අආ😀x", 3); got != "අආ" {
		t.Errorf("sliceUTF16 = %q", got)
	}
}

// ---- store (ported from test/store.test.ts) --------------------------------------------------

func testStore(t *testing.T) *SiteStore {
	t.Helper()
	appRoot = t.TempDir()
	os.MkdirAll(filepath.Join(appRoot, "config"), 0o755)
	os.WriteFile(filepath.Join(appRoot, "config", "library.json"), []byte(`{"fileTypeName":"පොත්","rootFolderName":"පුස්තකාලය","webUrlRoot":"https://tipitaka.lk/library/","s3RootFolder":"library"}`), 0o644)
	siteConfigs = map[string]*SiteConfig{}
	db, err := openDBFile(filepath.Join(appRoot, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	mk := func(e Entry) *Entry {
		if e.Kind == "" {
			e.Kind, e.Type = "file", "pdf"
		}
		e.S3Key, e.SizeBytes, e.IsPublished, e.DateAdded = e.Name+".pdf", 100, !e.IsPublished, "2026-01-01" // IsPublished inverted: zero value = published
		return &e
	}
	if err := db.SaveMany([]*Entry{
		mk(Entry{ID: 1, Name: "අභිධර්ම", Kind: "folder", Type: "folder"}),
		mk(Entry{ID: 2, Name: "හිමි පොත", ParentID: 1, Downloads: 5}),
		mk(Entry{ID: 3, Name: "English book", ParentID: 1}),
		mk(Entry{ID: 4, Name: "hidden folder", Kind: "folder", Type: "folder", IsPublished: true}),
		mk(Entry{ID: 5, Name: "inside hidden", ParentID: 4}),
		mk(Entry{ID: 6, Name: "gone", ParentID: 1, MissingSince: "2026-02-01"}),
	}); err != nil {
		t.Fatal(err)
	}
	s, err := newSiteStore("library", db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestStoreTotalsAndVisibility(t *testing.T) {
	s := testStore(t)
	if f := s.Get(1); f == nil || f.NumEntries != 2 || f.SizeBytes != 200 {
		t.Errorf("folder 1 = %+v", f)
	}
	if s.Get(4) != nil || s.Get(5) != nil {
		t.Error("an unpublished folder hides its subtree")
	}
	if s.Get(6) != nil {
		t.Error("missing files are hidden")
	}
	if c := s.ChildrenOf(0); len(c) != 1 || c[0].ID != 1 {
		t.Errorf("root children %v", c)
	}
	if p := s.Parents(2); len(p) != 1 || p[0].ID != 1 {
		t.Errorf("parents %v", p)
	}
}

func TestStoreSearch(t *testing.T) {
	s := testStore(t)
	if r := s.Search("ENGLISH", searchMaxResults); len(r.Entries) != 1 || r.Entries[0].ID != 3 {
		t.Errorf("ENGLISH = %v", r.Entries)
	}
	if r := s.Search("inside", searchMaxResults); len(r.Entries) != 0 {
		t.Error("hidden entries must not be found")
	}
	if getPossibleMatches("himi") == nil {
		t.Skip("singlish matching needs the Go port of singlish-search")
	}
	if r := s.Search("himi", searchMaxResults); len(r.Entries) != 1 || r.Entries[0].ID != 2 {
		t.Errorf("himi = %v", r.Entries)
	}
}

func TestStoreHits(t *testing.T) {
	s := testStore(t)
	if !s.Hit(2, "a") || s.Hit(2, "a") || !s.Hit(2, "b") {
		t.Error("hits are counted once per client per 10 minutes")
	}
	all, _ := s.DB.All()
	for _, e := range all {
		if e.ID == 2 && e.Downloads != 7 {
			t.Errorf("downloads %d", e.Downloads)
		}
	}
	if s.Hit(1, "a") {
		t.Error("folders are not counted")
	}
}
