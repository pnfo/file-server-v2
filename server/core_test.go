package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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

const (
	testLinkHost   = "library.tipitaka.lk"
	testLinkSecret = "SECRET"
)

// testdata/link_vectors.json is also checked by the Worker (worker/test.mjs), which recomputes every signature
// with its own code. UPDATE_VECTORS=1 rewrites the "out" values after a deliberate change to the link format.
func TestSignLinkVectors(t *testing.T) {
	const file = "testdata/link_vectors.json"
	var v struct {
		Host   string `json:"host"`
		Secret string `json:"secret"`
		NowMs  int64  `json:"nowMs"`
		Links  []struct {
			ObjectKey string     `json:"objectKey"`
			FileName  string     `json:"fileName"`
			Mode      string     `json:"mode"`
			Out       SignedLink `json:"out"`
		} `json:"links"`
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &v); err != nil || len(v.Links) == 0 {
		t.Fatalf("%s: %v", file, err)
	}
	update := os.Getenv("UPDATE_VECTORS") != ""
	for i, l := range v.Links {
		got := signLink(SignInput{Host: v.Host, ObjectKey: l.ObjectKey, FileName: l.FileName, Mode: LinkMode(l.Mode), Secret: v.Secret, NowMs: v.NowMs})
		if update {
			v.Links[i].Out = got
		} else if got != l.Out {
			t.Errorf("signLink(%s)\n got %+v\nwant %+v", l.ObjectKey, got, l.Out)
		}
	}
	if update {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", " ")
		enc.Encode(v)
		if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSignLinkSignature(t *testing.T) {
	nowMs := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	l := signLink(SignInput{Host: testLinkHost, ObjectKey: "library/අ{1}/x[v]{2}.pdf", FileName: "x - v.pdf", Mode: "inline", Secret: testLinkSecret, NowMs: nowMs})
	u, err := url.Parse(l.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != testLinkHost {
		t.Errorf("host %s", u.Host)
	}
	if p, _ := url.PathUnescape(u.EscapedPath()); p != "/library/අ{1}/x[v]{2}.pdf" {
		t.Errorf("path %s", p)
	}
	cd := u.Query().Get("response-content-disposition")
	if cd != `inline; filename="file.pdf"; filename*=UTF-8''x%20-%20v.pdf` {
		t.Errorf("disposition %s", cd)
	}
	sts := "GET\n" + u.Query().Get("Expires") + "\n" + u.EscapedPath() + "\n" + cd
	m := hmac.New(sha256.New, []byte(testLinkSecret))
	m.Write([]byte(sts))
	if u.Query().Get("Signature") != base64.RawURLEncoding.EncodeToString(m.Sum(nil)) {
		t.Error("signature mismatch")
	}
	// a stream link has no Content-Disposition override
	if s := signLink(SignInput{Host: testLinkHost, ObjectKey: "library/x{3}.m4a", FileName: "x.m4a", Mode: "stream", Secret: testLinkSecret, NowMs: nowMs}); strings.Contains(s.URL, "disposition") {
		t.Errorf("stream link %s", s.URL)
	}
}

func TestPublicLink(t *testing.T) {
	l := publicLink("cdn.tipitaka.lk", "cloud/අ{1}/a b{2}.mp3", "stream")
	if l.URL != "https://cdn.tipitaka.lk/cloud/%E0%B6%85%7B1%7D/a%20b%7B2%7D.mp3" || l.Expires != 0 {
		t.Errorf("public link %+v", l)
	}
	if l := publicLink("cdn.tipitaka.lk", "cloud/x{3}.zip", "attachment"); l.URL != "https://cdn.tipitaka.lk/cloud/x%7B3%7D.zip?dl=1" {
		t.Errorf("attachment link %+v", l)
	}
}

func TestBucketFor(t *testing.T) {
	siteConfigsMu.Lock()
	old := siteConfigs
	siteConfigs = map[string]*SiteConfig{
		"library": {ID: "library", S3RootFolder: "library", Bucket: "priv", Signed: true},
		"cloud":   {ID: "cloud", S3RootFolder: "cloud", Bucket: "pub"},
	}
	siteConfigsMu.Unlock()
	t.Cleanup(func() { siteConfigsMu.Lock(); siteConfigs = old; siteConfigsMu.Unlock() })
	for key, want := range map[string]string{"library/a{1}.pdf": "priv", "library/": "priv", "cloud/a{1}.mp3": "pub",
		dbBackupKey(getSiteConfig("library"), "library-x.db"): "priv", dbBackupKey(getSiteConfig("cloud"), "cloud-x.db"): "priv"} {
		if got := bucketFor(key); got != want {
			t.Errorf("bucketFor(%s) = %s, want %s", key, got, want)
		}
	}
	if k := dbBackupKey(getSiteConfig("cloud"), "cloud-x.db"); k != "cloud-db-backups/cloud-x.db" {
		t.Errorf("cloud snapshot key %s", k)
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
	os.WriteFile(filepath.Join(appRoot, "config", "library.json"), []byte(`{"fileTypeName":"පොත්","rootFolderName":"පුස්තකාලය","webUrlRoot":"https://tipitaka.lk/library/","s3RootFolder":"library","bucket":"tipitaka-library","publicHost":"library.tipitaka.lk","signed":true}`), 0o644)
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

// ---- editions --------------------------------------------------------------------------------

func TestMigrationAddsEditionGroup(t *testing.T) {
	// a database from before the editions migration (user_version 1), with a row in it
	file := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", "file:"+file)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{migrations[0], "PRAGMA user_version = 1",
		`INSERT INTO entries (id, kind, type, name, s3_key, date_added, updated_at) VALUES (2, 'file', 'pdf', 'පොත', 'පොත{2}.pdf', '2026-01-01', '2026-01-01')`} {
		if _, err := old.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()
	db, err := openDBFile(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, indexes int
	db.db.QueryRow("PRAGMA user_version").Scan(&version)
	db.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'entries_edition_group'").Scan(&indexes)
	if version != len(migrations) || indexes != 1 {
		t.Errorf("user_version %d, index %d", version, indexes)
	}
	all, err := db.All()
	if err != nil || len(all) != 1 || all[0].Name != "පොත" || all[0].EditionGroup != 0 {
		t.Fatalf("rows %+v %v", all, err)
	}
}

func TestEditionGroupRoundTrip(t *testing.T) {
	db, err := openDBFile(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := &Entry{ID: 9, Kind: "file", Type: "pdf", Name: "x", S3Key: "x{9}.pdf", IsPublished: true, DateAdded: "2026-01-01", EditionGroup: 4}
	if err := db.Save(e); err != nil {
		t.Fatal(err)
	}
	all, _ := db.All()
	if len(all) != 1 || all[0].EditionGroup != 4 {
		t.Fatalf("saved %+v", all)
	}
	if b, _ := all[0].MarshalJSON(); !strings.Contains(string(b), `"editionGroup":4`) {
		t.Errorf("json %s", b)
	}
	e.EditionGroup = 0
	db.Save(e)
	all, _ = db.All()
	if b, _ := all[0].MarshalJSON(); all[0].EditionGroup != 0 || !strings.Contains(string(b), `"editionGroup":null`) {
		t.Errorf("cleared: %s", b)
	}
}

// editionStore is testStore plus files 7-9 in folder 1 (9 unpublished).
func editionStore(t *testing.T) *SiteStore {
	t.Helper()
	s := testStore(t)
	var extra []*Entry
	for _, e := range []Entry{{ID: 7, Name: "b edition", PublishingYear: 1990}, {ID: 8, Name: "a edition", PublishingYear: 1990},
		{ID: 9, Name: "draft", PublishingYear: 1950}} {
		e.ParentID, e.Kind, e.Type, e.S3Key, e.IsPublished, e.DateAdded = 1, "file", "pdf", e.Name+".pdf", e.ID != 9, "2026-01-01"
		extra = append(extra, &e)
	}
	if err := s.DB.SaveMany(extra); err != nil {
		t.Fatal(err)
	}
	if err := s.reload(); err != nil {
		t.Fatal(err)
	}
	return s
}

func groupOf(t *testing.T, s *SiteStore) map[int]int {
	t.Helper()
	all, err := s.DB.All()
	if err != nil {
		t.Fatal(err)
	}
	g := map[int]int{}
	for _, e := range all {
		if e.EditionGroup != 0 {
			g[e.ID] = e.EditionGroup
		}
	}
	return g
}

func TestSetEditions(t *testing.T) {
	s := editionStore(t)
	check := func(res orderedMap, err error, group int, ids ...int) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := marshalJSON(res)
		want, _ := marshalJSON(orderedMap{{"editionGroup", nullInt(group)}, {"ids", append([]int{}, ids...)}})
		if string(b) != string(want) {
			t.Errorf("got %s, want %s", b, want)
		}
	}
	res, err := s.SetEditions([]int{8, 7}, false)
	check(res, err, 7, 7, 8)
	res, err = s.SetEditions([]int{3, 2, 3}, false)
	check(res, err, 2, 2, 3)
	// 8 and 3 are in different groups: both groups merge into one, numbered by the smallest id
	res, err = s.SetEditions([]int{8, 3, 9}, false)
	check(res, err, 2, 2, 3, 7, 8, 9)
	if g := groupOf(t, s); len(g) != 5 || g[7] != 2 || g[9] != 2 {
		t.Errorf("saved groups %v", g)
	}
	// taking out the smallest id renumbers the rest
	res, err = s.SetEditions([]int{2}, true)
	check(res, err, 3, 3, 7, 8, 9)
	if g := groupOf(t, s); len(g) != 4 || g[2] != 0 || g[8] != 3 {
		t.Errorf("after clear %v", g)
	}
	// a group of one is no group
	s.SetEditions([]int{3}, true)
	s.SetEditions([]int{7}, true)
	res, err = s.SetEditions([]int{8}, true)
	check(res, err, 0)
	if g := groupOf(t, s); len(g) != 0 {
		t.Errorf("after clearing all %v", g)
	}
	for _, c := range []struct {
		ids   []int
		clear bool
	}{{[]int{2}, false}, {[]int{2, 2}, false}, {[]int{2, 1}, false}, {[]int{2, 99}, false}, {[]int{2, 3}, true}, {nil, true}} {
		var ae *AdminError
		if _, err := s.SetEditions(c.ids, c.clear); !errors.As(err, &ae) {
			t.Errorf("SetEditions(%v, %v) = %v, want an AdminError", c.ids, c.clear, err)
		}
	}
}

func TestSaveMetaEditionGroup(t *testing.T) {
	s := editionStore(t)
	if e, err := s.SaveMeta(2, body{"editionGroup": float64(2)}); err != nil || e.EditionGroup != 2 {
		t.Fatalf("save %+v %v", e, err)
	}
	if e, err := s.SaveMeta(2, body{"author": "x"}); err != nil || e.EditionGroup != 2 {
		t.Errorf("a save without editionGroup keeps it: %+v %v", e, err)
	}
	if e, err := s.SaveMeta(2, body{"editionGroup": nil}); err != nil || e.EditionGroup != 0 {
		t.Errorf("null clears it: %+v %v", e, err)
	}
	if _, err := s.SaveMeta(1, body{"editionGroup": float64(1)}); err == nil {
		t.Error("folders have no editions")
	}
}

func TestStoreEditions(t *testing.T) {
	s := editionStore(t)
	// 5 is in an unpublished folder, 6 is missing from the bucket, 9 is unpublished
	if _, err := s.SetEditions([]int{2, 3, 5, 6, 7, 8, 9}, false); err != nil {
		t.Fatal(err)
	}
	ids := func(list []*Entry) []int {
		var out []int
		for _, e := range list {
			out = append(out, e.ID)
		}
		return out
	}
	// by year (no year last), then by name
	if got := ids(s.Editions(s.GetAny(3))); !slices.Equal(got, []int{8, 7, 2}) {
		t.Errorf("editions of 3 = %v", got)
	}
	if got := ids(s.Editions(s.GetAny(2))); !slices.Equal(got, []int{8, 7, 3}) {
		t.Errorf("editions of 2 = %v", got)
	}
	if got := ids(s.editions(s.GetAny(2), false)); !slices.Equal(got, []int{9, 8, 7, 3, 6, 5}) {
		t.Errorf("admin editions of 2 = %v", got)
	}
	if s.Editions(s.GetAny(1)) != nil {
		t.Error("an entry without a group has no editions")
	}
}

func TestEditionsOnPages(t *testing.T) {
	s := editionStore(t)
	os.WriteFile(filepath.Join(appRoot, "config", "secrets.json"), []byte(`{"s3":{},"linkSecret":"x"}`), 0o644)
	loadTemplates()
	if _, err := s.SetEditions([]int{2, 7, 9}, false); err != nil {
		t.Fatal(err)
	}
	site := &SiteCtx{ID: "library", Base: "/library", Cfg: s.Cfg, Store: s}
	w := httptest.NewRecorder()
	pageEntry(w, httptest.NewRequest("GET", "/library/2", nil), site, "2")
	html := w.Body.String()
	if w.Code != 200 || !strings.Contains(html, "වෙනත් සංස්කරණ") || !strings.Contains(html, `href="/library/7"`) || strings.Contains(html, `href="/library/9"`) {
		t.Errorf("file page %d:\n%s", w.Code, html)
	}
	w = httptest.NewRecorder()
	pageEntry(w, httptest.NewRequest("GET", "/library/3", nil), site, "3")
	if strings.Contains(w.Body.String(), "වෙනත් සංස්කරණ") {
		t.Error("no editions section without a group")
	}
}

func TestSaveMetaMove(t *testing.T) {
	s := testStore(t)
	var moves [][2]string
	s3Move = func(from, to string) error { moves = append(moves, [2]string{from, to}); return nil }
	t.Cleanup(func() { s3Move = s3MoveObject })
	if err := s.DB.SaveMany([]*Entry{
		{ID: 10, Kind: "folder", Type: "folder", Name: "A", S3Key: "A{10}", IsPublished: true, DateAdded: "2026-01-01"},
		{ID: 11, ParentID: 10, Kind: "folder", Type: "folder", Name: "B", S3Key: "A{10}/B{11}", IsPublished: true, DateAdded: "2026-01-01"},
		{ID: 12, ParentID: 10, Kind: "file", Type: "pdf", Name: "book", S3Key: "A{10}/book{12}.pdf", IsPublished: true, DateAdded: "2026-01-01"},
		{ID: 13, Kind: "folder", Type: "folder", Name: "C", S3Key: "C{13}", IsPublished: true, DateAdded: "2026-01-01"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.reload(); err != nil {
		t.Fatal(err)
	}
	isAdminErr := func(err error) bool { var ae *AdminError; return errors.As(err, &ae) }

	// a save without parentId never moves
	if e, err := s.SaveMeta(12, body{"author": "x"}); err != nil || e.S3Key != "A{10}/book{12}.pdf" || len(moves) != 0 {
		t.Fatalf("plain save: %+v %v %v", e, err, moves)
	}
	// into a subfolder: same base name under the new folder's path
	e, err := s.SaveMeta(12, body{"parentId": float64(11)})
	if err != nil || e.ParentID != 11 || e.S3Key != "A{10}/B{11}/book{12}.pdf" {
		t.Fatalf("move: %+v %v", e, err)
	}
	if len(moves) != 1 || moves[0] != [2]string{"library/A{10}/book{12}.pdf", "library/A{10}/B{11}/book{12}.pdf"} {
		t.Errorf("s3 moves %v", moves)
	}
	if got := s.GetAny(12); got.ParentID != 11 || got.S3Key != e.S3Key {
		t.Errorf("not reloaded: %+v", got)
	}
	// move and rename at once: one object move
	moves = nil
	e, err = s.SaveMeta(12, body{"parentId": float64(13), "name": "new", "version": "v2"})
	want := "C{13}/" + buildFileName("new", "v2", 12, "pdf")
	if err != nil || e.ParentID != 13 || e.S3Key != want || len(moves) != 1 || moves[0][1] != "library/"+want {
		t.Fatalf("move+rename: %+v %v %v", e, err, moves)
	}
	// to the top level
	if e, err = s.SaveMeta(12, body{"parentId": float64(0)}); err != nil || e.ParentID != 0 || e.S3Key != buildFileName("new", "v2", 12, "pdf") {
		t.Errorf("to root: %+v %v", e, err)
	}
	// an empty folder moves without touching the bucket; a folder with contents does not move
	moves = nil
	if e, err = s.SaveMeta(13, body{"parentId": float64(10)}); err != nil || e.S3Key != "A{10}/C{13}" || len(moves) != 0 {
		t.Errorf("empty folder: %+v %v %v", e, err, moves)
	}
	if _, err = s.SaveMeta(10, body{"parentId": float64(13)}); !isAdminErr(err) {
		t.Errorf("folder with contents moved: %v", err)
	}
	// targets must be folders; nothing moves into itself
	for _, c := range []struct{ id, to int }{{12, 2}, {12, 99}, {13, 13}} {
		if _, err := s.SaveMeta(c.id, body{"parentId": float64(c.to)}); !isAdminErr(err) {
			t.Errorf("move %d -> %d: %v, want an AdminError", c.id, c.to, err)
		}
	}
}
