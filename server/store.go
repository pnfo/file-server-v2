package main

import (
	"bytes"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	searchMaxResults = 50
	searchMaxTerms   = 300
)

var (
	dropletBase  = strings.TrimSuffix(os.Getenv("DROPLET_BASE"), "/") // e.g. https://tipitaka.lk/fs-dl
	dropletShare = func() float64 {
		n, _ := strconv.ParseFloat(os.Getenv("DROPLET_SHARE"), 64)
		return min(100, max(0, n))
	}()
)

// byName: folders first, then Sinhala collation, then id.
func byName(a, b *Entry) bool {
	if a.Kind != b.Kind {
		return a.IsFolder()
	}
	if c := bytes.Compare(a.sortKey, b.sortKey); c != 0 {
		return c < 0
	}
	return a.ID < b.ID
}

// SiteStore: everything a public page reads comes from memory; SQLite is only persistence.
// Rebuilt from the DB after every admin write or sync (a few thousand rows, a few ms).
//
// Locking: mu guards the maps and the entries in them. Page handlers hold RLock while rendering; hit() takes
// the write lock to bump a counter; reload() builds new maps and swaps them in under the write lock. writeMu
// serialises admin writes and syncs (reserved ids, read-modify-write of entries).
type SiteStore struct {
	Site string
	Cfg  *SiteConfig
	DB   *SiteDB

	mu       sync.RWMutex
	byID     map[int]*Entry
	ordered  []*Entry // all entries, byName
	children map[int][]*Entry
	blocked  map[int]bool
	loadedAt time.Time

	recentHits map[string]time.Time

	writeMu  sync.Mutex
	reserved map[int]bool // ids handed out for uploads in progress
}

func newSiteStore(site string, db *SiteDB) (*SiteStore, error) {
	s := &SiteStore{Site: site, Cfg: getSiteConfig(site), DB: db, recentHits: map[string]time.Time{}, reserved: map[int]bool{}}
	return s, s.reload()
}

func (s *SiteStore) reload() error {
	all, err := s.DB.All()
	if err != nil {
		return err
	}
	byID := make(map[int]*Entry, len(all))
	coll := newCollator()
	for _, e := range all {
		byID[e.ID] = e
		e.sortKey = coll.Key(e.Name)
	}
	// an unpublished folder hides its whole subtree
	blocked := map[int]bool{}
	for _, e := range all {
		for p, guard := byID[e.ParentID], 0; p != nil && guard < 50; p, guard = byID[p.ParentID], guard+1 {
			if !p.IsPublished {
				blocked[e.ID] = true
				break
			}
		}
	}
	// folder totals: published files that are not missing, rolled up through every ancestor
	for _, f := range all {
		if !f.IsFile() || !f.IsPublished || f.MissingSince != "" || blocked[f.ID] {
			continue
		}
		for p, guard := byID[f.ParentID], 0; p != nil && guard < 50; p, guard = byID[p.ParentID], guard+1 {
			p.NumEntries++
			p.SizeBytes += f.SizeBytes
		}
	}
	visible := func(e *Entry) bool {
		return e.IsPublished && e.MissingSince == "" && !blocked[e.ID] && (e.IsFile() || e.NumEntries > 0)
	}
	children := map[int][]*Entry{}
	for _, e := range all {
		if visible(e) {
			children[e.ParentID] = append(children[e.ParentID], e)
		}
	}
	for _, list := range children {
		sort.SliceStable(list, func(i, j int) bool { return byName(list[i], list[j]) })
	}
	ordered := append([]*Entry(nil), all...)
	sort.SliceStable(ordered, func(i, j int) bool { return byName(ordered[i], ordered[j]) })

	s.mu.Lock()
	s.byID, s.ordered, s.children, s.blocked, s.loadedAt = byID, ordered, children, blocked, time.Now()
	s.mu.Unlock()
	return nil
}

// The methods below expect the caller to hold s.mu (RLock is enough).

func (s *SiteStore) isVisible(e *Entry) bool {
	return e.IsPublished && e.MissingSince == "" && !s.blocked[e.ID] && (e.IsFile() || e.NumEntries > 0)
}

func (s *SiteStore) Size() int { return len(s.byID) }

// Get returns a published entry, or nil (unpublished/missing entries are 404 on public pages).
func (s *SiteStore) Get(id int) *Entry {
	if e := s.byID[id]; e != nil && s.isVisible(e) {
		return e
	}
	return nil
}
func (s *SiteStore) GetAny(id int) *Entry       { return s.byID[id] }
func (s *SiteStore) ChildrenOf(id int) []*Entry { return s.children[id] }
func (s *SiteStore) ParentOf(e *Entry) *Entry   { return s.byID[e.ParentID] }
func (s *SiteStore) Ordered() []*Entry          { return s.ordered }
func (s *SiteStore) HasChildren(id int) bool    { return s.anyChild(id) }
func (s *SiteStore) anyChild(id int) bool {
	for _, e := range s.ordered {
		if e.ParentID == id {
			return true
		}
	}
	return false
}

// Parents returns the ancestors from the root down, excluding the entry itself.
func (s *SiteStore) Parents(id int) []*Entry {
	var chain []*Entry
	start := 0
	if e := s.byID[id]; e != nil {
		start = e.ParentID
	}
	for p, guard := s.byID[start], 0; p != nil && guard < 50; p, guard = s.byID[p.ParentID], guard+1 {
		chain = append([]*Entry{p}, chain...)
	}
	return chain
}

type SearchResult struct {
	Entries      []*Entry
	TooManyTerms bool
}

func (s *SiteStore) Search(query string, maxResults int) SearchResult {
	q := strings.ToLower(jsTrim(query))
	if q == "" {
		return SearchResult{}
	}
	terms := getPossibleMatches(q)
	if len(terms) > searchMaxTerms {
		return SearchResult{TooManyTerms: true}
	}
	// the Node server matched one case-insensitive regex of all terms; substring tests on lowercased text are the same
	needles := make([]string, 0, len(terms)+1)
	for _, t := range append(terms, q) {
		if t != "" {
			needles = append(needles, strings.ToLower(t))
		}
	}
	match := func(field string) bool {
		if field == "" {
			return false
		}
		f := strings.ToLower(field)
		for _, n := range needles {
			if strings.Contains(f, n) {
				return true
			}
		}
		return false
	}
	var out []*Entry
	for _, e := range s.ordered {
		if !s.isVisible(e) {
			continue
		}
		if match(e.Name) || match(e.Version) || match(e.Author) {
			out = append(out, e)
			if len(out) >= maxResults {
				break
			}
		}
	}
	return SearchResult{Entries: out}
}

func (s *SiteStore) Recent(days int) []*Entry {
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour).UTC().Format("2006-01-02")
	var out []*Entry
	for _, e := range s.byIDOrder() {
		if e.IsFile() && s.isVisible(e) && e.DateAdded > since {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].DateAdded != out[j].DateAdded {
			return out[i].DateAdded > out[j].DateAdded
		}
		return bytes.Compare(out[i].sortKey, out[j].sortKey) < 0
	})
	return out
}

// byIDOrder is every entry in database order (the order the Node server's Map iterated in).
func (s *SiteStore) byIDOrder() []*Entry {
	out := append([]*Entry(nil), s.ordered...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Hit counts a download, ignoring repeats of the same (client, file) within 10 minutes. Takes the write lock.
func (s *SiteStore) Hit(id int, clientKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.Get(id)
	if e == nil || !e.IsFile() {
		return false
	}
	key := clientKey + "|" + strconv.Itoa(id)
	now := time.Now()
	if last, ok := s.recentHits[key]; ok && now.Sub(last) < 10*time.Minute {
		return false
	}
	if len(s.recentHits) > 20_000 {
		s.recentHits = map[string]time.Time{}
	}
	s.recentHits[key] = now
	if err := s.DB.Hit(id); err != nil {
		log.Printf("[%s] hit %d: %v", s.Site, id, err)
		return false
	}
	e.Downloads++
	return true
}

func (s *SiteStore) Link(e *Entry, mode LinkMode) SignedLink {
	k := getSecrets().Signer
	// DROPLET_SHARE % of links go through the droplet's nginx proxy instead of the CDN, to use the droplet
	// transfer pool (Spaces -> Droplet in SGP1 is free) and keep Spaces egress under its allowance.
	base := ""
	if dropletBase != "" && rand.Float64()*100 < dropletShare {
		base = dropletBase
	}
	return signLink(SignInput{ObjectKey: s.Cfg.S3RootFolder + "/" + e.S3Key, FileName: saveAsName(e), Mode: mode,
		AccessKeyID: k.AccessKeyID, SecretAccessKey: k.SecretAccessKey, NowMs: time.Now().UnixMilli(), BaseURL: base})
}

type Preview struct {
	Page int // 0 = unknown
	URL  string
}

// Previews are the public image URLs for the PDF preview gallery.
func (s *SiteStore) Previews(e *Entry) []Preview {
	if e.Type != "pdf" {
		return nil
	}
	base := "https://" + cdnHost + "/" + s.Cfg.S3RootFolder
	var out []Preview
	if len(e.PreviewPages) > 0 {
		for i, p := range e.PreviewPages {
			out = append(out, Preview{p, fmt.Sprintf("%s/previews/%d-%d.jpg?v=%s", base, e.ID, i, e.UpdatedAt)})
		}
		return out
	}
	if !s.Cfg.DisplayThumbs {
		return nil
	}
	// old thumbnails (thumb-generator.js): page 1 and the middle page, floor(n/2) counted from 0; one image for 1-page PDFs
	pages := []int{0, 0}
	if n := e.NumPages; n > 1 {
		pages = []int{1, n/2 + 1}
	} else if n == 1 {
		pages = []int{1}
	}
	for i, p := range pages {
		out = append(out, Preview{p, fmt.Sprintf("%s/thumbs/%d-%d.jpg", base, e.ID, i)})
	}
	return out
}

func (s *SiteStore) nextID() int {
	n := 0
	for id := range s.byID {
		n = max(n, id)
	}
	return n + 1
}

var (
	stores   = map[string]*SiteStore{}
	storesMu sync.Mutex
)

func getStore(site string) *SiteStore {
	storesMu.Lock()
	defer storesMu.Unlock()
	if s := stores[site]; s != nil {
		return s
	}
	db, err := openSiteDB(site)
	if err != nil {
		log.Fatalf("[%s] open db: %v", site, err)
	}
	s, err := newSiteStore(site, db)
	if err != nil {
		log.Fatalf("[%s] load: %v", site, err)
	}
	log.Printf("[%s] loaded %d entries", site, s.Size())
	if s.Size() == 0 {
		log.Printf(`[%s] database is empty - run "file_server sync %s"`, site, site)
	}
	stores[site] = s
	return s
}
