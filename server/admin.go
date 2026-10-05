package main

// Admin operations. Upload bytes never pass through this server: the browser PUTs straight to Spaces with a
// presigned URL, then calls commit (design §7). Every write rebuilds the in-memory index.

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// AdminError is a user-facing error (400); anything else is a 500.
type AdminError struct{ msg string }

func (e *AdminError) Error() string          { return e.msg }
func adminErr(format string, a ...any) error { return &AdminError{fmt.Sprintf(format, a...)} }

// body is a decoded JSON request body; has() tells "missing" (undefined) from null.
type body map[string]any

func (b body) has(k string) bool    { _, ok := b[k]; return ok }
func (b body) num(k string) float64 { v, ok := b[k]; return jsNumber(v, ok) }
func (b body) intOr0(k string) int  { return orZero(b.num(k)) }
func (b body) str(k string) string  { s, _ := b[k].(string); return s }
func (b body) truthy(k string) bool { return jsTruthy(b[k]) }

// orZero is Number(x) || 0 for an integer field.
func orZero(f float64) int {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return int(f)
}

func (s *SiteStore) objectKey(rel string) string { return s.Cfg.S3RootFolder + "/" + rel }

// parentPath expects s.mu held.
func (s *SiteStore) parentPath(parentID int) (string, error) {
	if parentID == 0 {
		return "", nil
	}
	p := s.GetAny(parentID)
	if p == nil || !p.IsFolder() {
		return "", adminErr("folder %d not found", parentID)
	}
	return p.S3Key + "/", nil
}

// cleanText: a non-empty trimmed string, cut to max UTF-16 units, or "" (null).
func cleanText(v any, max int) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return sliceUTF16(jsTrim(s), max)
}

func year(v any, present bool) int {
	n := jsNumber(v, present)
	if n == math.Trunc(n) && n > 0 && n < 3000 {
		return int(n)
	}
	return 0
}

// reserveID expects writeMu held.
func (s *SiteStore) reserveID() int {
	s.mu.RLock()
	id := s.nextID()
	s.mu.RUnlock()
	for s.reserved[id] {
		id++
	}
	s.reserved[id] = true
	return id
}

// nanID stands for an id that was not a number (the Node server reported it as NaN).
const nanID = math.MinInt32

// entryCopy returns a copy of entry id, or an AdminError.
func (s *SiteStore) entryCopy(id int) (Entry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e := s.GetAny(id)
	if e == nil {
		if id == nanID {
			return Entry{}, adminErr("entry NaN not found")
		}
		return Entry{}, adminErr("entry %d not found", id)
	}
	return *e, nil
}

func (s *SiteStore) hasChildrenLocked(id int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.anyChild(id)
}

func (s *SiteStore) touch(e *Entry) *Entry { e.UpdatedAt = nowISO(); return e }

func (s *SiteStore) saveAndReload(e *Entry) error {
	if err := s.DB.Save(e); err != nil {
		return err
	}
	return s.reload()
}

func (s *SiteStore) SaveMeta(id int, in body) (*Entry, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	e, err := s.entryCopy(id)
	if err != nil {
		return nil, err
	}
	next := e
	if in.has("name") {
		if next.Name = cleanText(in["name"], 300); next.Name == "" {
			return nil, adminErr("name is required")
		}
	}
	if in.has("version") {
		next.Version = cleanText(in["version"], 100)
	}
	if in.has("author") {
		next.Author = cleanText(in["author"], 300)
	}
	if in.has("publishingYear") {
		next.PublishingYear = year(in["publishingYear"], true)
	}
	if in.has("printedYear") {
		next.PrintedYear = year(in["printedYear"], true)
	}
	if in.has("description") {
		next.Description = sanitizeHTML(in.str("description"))
	}
	if in.has("isPublished") {
		next.IsPublished = in.truthy("isPublished")
	}
	// name and version live in the S3 key, so changing them renames the object
	if next.Name != e.Name || next.Version != e.Version {
		dir := ""
		if i := strings.LastIndex(e.S3Key, "/"); i >= 0 {
			dir = e.S3Key[:i+1]
		}
		if e.IsFolder() {
			if s.hasChildrenLocked(e.ID) {
				return nil, adminErr("folders with contents cannot be renamed here (every file key would change) - rename in the bucket and run sync")
			}
			next.S3Key = dir + buildFolderName(next.Name, e.ID)
		} else {
			next.S3Key = dir + buildFileName(next.Name, next.Version, e.ID, e.Type)
			if err := s3Move(s.objectKey(e.S3Key), s.objectKey(next.S3Key)); err != nil {
				return nil, err
			}
		}
	}
	return &next, s.saveAndReload(s.touch(&next))
}

func (s *SiteStore) CreateFolder(parentID int, name any) (*Entry, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n := cleanText(name, 300)
	if n == "" {
		return nil, adminErr("name is required")
	}
	id := s.reserveID()
	defer delete(s.reserved, id)
	s.mu.RLock()
	dir, err := s.parentPath(parentID)
	s.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	e := &Entry{ID: id, ParentID: parentID, Kind: "folder", Type: "folder", Name: n, S3Key: dir + buildFolderName(n, id),
		IsPublished: true, DateAdded: today(), UpdatedAt: nowISO()}
	return e, s.saveAndReload(e)
}

func (s *SiteStore) StartUpload(b body) (orderedMap, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	parsed := parseFileName(fileNameOf(b))
	if parsed.Type == "" {
		return nil, adminErr("file has no extension")
	}
	typ := strings.ToLower(parsed.Type)
	name := cleanText(b["name"], 300)
	if name == "" {
		name = parsed.Name
	}
	version := parsed.Version
	if b.has("version") {
		version = cleanText(b["version"], 100)
	}
	s.mu.RLock()
	dir, err := s.parentPath(b.intOr0("parentId"))
	s.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	id := s.reserveID()
	rel := dir + buildFileName(name, version, id, typ)
	contentType := firstNonEmpty(b.str("contentType"), getTypeInfo(typ).Mime, "application/octet-stream")
	u, err := s3PresignPut(s.objectKey(rel), contentType, false)
	if err != nil {
		delete(s.reserved, id)
		return nil, err
	}
	return orderedMap{{"id", id}, {"key", rel}, {"name", name}, {"version", nullStr(version)}, {"type", typ},
		{"contentType", contentType}, {"url", u}}, nil
}

// fileNameOf is String(b.fileName || ”).
func fileNameOf(b body) string {
	if !jsTruthy(b["fileName"]) {
		return ""
	}
	if s, ok := b["fileName"].(string); ok {
		return s
	}
	return fmt.Sprint(b["fileName"])
}

func (s *SiteStore) CommitUpload(b body) (*Entry, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	id := orZero(b.num("id"))
	if !s.reserved[id] {
		return nil, adminErr("unknown upload - start again")
	}
	key := b.str("key")
	if !s3Exists(s.objectKey(key)) {
		return nil, adminErr("the file did not reach the bucket")
	}
	published := true
	if v, ok := b["isPublished"]; ok && v != nil {
		published = jsTruthy(v)
	}
	e := &Entry{ID: id, ParentID: b.intOr0("parentId"), Kind: "file", Type: b.str("type"), Name: b.str("name"),
		Version: b.str("version"), Author: cleanText(b["author"], 300),
		PublishingYear: year(b["publishingYear"], b.has("publishingYear")), PrintedYear: year(b["printedYear"], b.has("printedYear")),
		Description: sanitizeHTML(b.str("description")), S3Key: key, SizeBytes: int64(orZero(b.num("size"))),
		IsPublished: published, DateAdded: today(), UpdatedAt: nowISO()}
	if err := s.saveAndReload(e); err != nil {
		return nil, err
	}
	delete(s.reserved, id)
	return e, nil
}

const maxPreviews = 6

func (s *SiteStore) previewKey(id, i int) string {
	return s.objectKey(fmt.Sprintf("previews/%d-%d.jpg", id, i))
}

func (s *SiteStore) PreviewUploadURLs(id int, count float64) ([]string, error) {
	e, err := s.entryCopy(id)
	if err != nil {
		return nil, err
	}
	if e.Type != "pdf" {
		return nil, adminErr("previews are for PDFs only")
	}
	if count < 1 || count > maxPreviews {
		return nil, adminErr("choose 1-%d pages", maxPreviews)
	}
	urls := []string{}
	if math.IsNaN(count) { // passes the checks above and gives no URLs, as in the Node server
		return urls, nil
	}
	for i := 0; i < int(count); i++ {
		u, err := s3PresignPut(s.previewKey(id, i), "image/jpeg", true)
		if err != nil {
			return nil, err
		}
		urls = append(urls, u)
	}
	return urls, nil
}

func (s *SiteStore) SavePreviews(id int, pages []any, numPages int) (*Entry, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	e, err := s.entryCopy(id)
	if err != nil {
		return nil, err
	}
	next := e
	next.PreviewPages = nil
	for _, p := range pages {
		next.PreviewPages = append(next.PreviewPages, orZero(jsNumber(p, true)))
	}
	if numPages != 0 {
		next.NumPages = numPages
	}
	if err := s.saveAndReload(s.touch(&next)); err != nil {
		return nil, err
	}
	// fewer pages than before: remove the images that are no longer used
	for i := len(pages); i < maxPreviews; i++ {
		s3Remove(s.previewKey(id, i))
	}
	return &next, nil
}

func (s *SiteStore) DeleteEntry(id int) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	e, err := s.entryCopy(id)
	if err != nil {
		return err
	}
	if e.IsFolder() {
		if s.hasChildrenLocked(e.ID) {
			return adminErr("folder is not empty")
		}
	} else {
		if err := s3Remove(s.objectKey(e.S3Key)); err != nil {
			return err
		}
		for i := 0; i < maxPreviews; i++ {
			s3Remove(s.previewKey(id, i))
		}
	}
	if err := s.DB.Delete(id); err != nil {
		return err
	}
	return s.reload()
}

func (s *SiteStore) SourceURL(id int) (string, error) {
	e, err := s.entryCopy(id)
	if err != nil {
		return "", err
	}
	return s3PresignGet(s.objectKey(e.S3Key))
}

func sortInts(a []int) { sort.Ints(a) }
