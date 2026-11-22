package main

// The admin API for scripts and AI sessions (docs/api.md): every request needs "Authorization: Bearer <token>",
// checked against secrets.json "apiTokens" ({name: token}). Failed tokens are rate limited per IP.
//
//   GET  entry?id=N           the entry, its folder path, and a folder's children (unpublished included)
//   GET  list?folder=N        a folder's children (0 = root)
//   GET  search?q=...&max=N   name / version / author search over every entry, unpublished included
//   GET  source-url?id=N      a presigned GET of the file (1 h)
//   POST actions: adminAPI in web.go. replace-url backs up the current object first; delete copies a file to
//   backups/deleted/ first.

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
)

const minTokenLen = 32

// bearerToken returns the token from an Authorization: Bearer header, and whether the header was there at all.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	scheme, tok, ok := strings.Cut(h, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", true
	}
	return strings.TrimSpace(tok), true
}

// apiTokenName returns the name of the configured token that matches, or "". Tokens shorter than minTokenLen are
// ignored so a placeholder in secrets.json can never open the API.
func apiTokenName(tok string) string {
	if len(tok) < minTokenLen {
		return ""
	}
	match := ""
	names := make([]string, 0, len(getSecrets().APITokens))
	for name := range getSecrets().APITokens {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names { // compare against every token, so timing doesn't tell which one is close
		want := getSecrets().APITokens[name]
		if len(want) >= minTokenLen && subtle.ConstantTimeCompare([]byte(tok), []byte(want)) == 1 {
			match = name
		}
	}
	return match
}

// ---- reads ------------------------------------------------------------------------------------

type apiCrumb struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func (s *SiteStore) crumbs(id int) []apiCrumb {
	out := []apiCrumb{}
	for _, p := range s.Parents(id) {
		out = append(out, apiCrumb{p.ID, p.Name})
	}
	return out
}

// childrenCopy lists a folder's children (published or not) in display order. Expects s.mu held.
func (s *SiteStore) childrenCopy(id int) []Entry {
	out := []Entry{}
	for _, c := range s.ChildrenOf(id) {
		out = append(out, *c)
	}
	return out
}

func (s *SiteStore) APIEntry(id int) (orderedMap, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e := s.GetAny(id)
	if e == nil {
		return nil, adminErr("entry %d not found", id)
	}
	res := orderedMap{{"entry", e}, {"path", s.crumbs(id)}}
	if e.IsFolder() {
		res = append(res, orderedMap{{"children", s.childrenCopy(id)}}...)
	}
	if e.EditionGroup != 0 {
		var ids []int
		for _, m := range s.editions(e, false) {
			ids = append(ids, m.ID)
		}
		res = append(res, orderedMap{{"editions", ids}}...)
	}
	return res, nil
}

func (s *SiteStore) APIList(folder int) (orderedMap, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if folder != 0 {
		if f := s.GetAny(folder); f == nil || !f.IsFolder() {
			return nil, adminErr("folder %d not found", folder)
		}
	}
	return orderedMap{{"folder", folder}, {"path", s.crumbs(folder)}, {"children", s.childrenCopy(folder)}}, nil
}

// APISearch is Search over every entry, unpublished and unlisted included.
func (s *SiteStore) APISearch(q string, maxResults int) []Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	needles := []string{}
	if q = strings.ToLower(jsTrim(q)); q == "" {
		return []Entry{}
	}
	for _, t := range append(getPossibleMatches(q), q) {
		if t != "" {
			needles = append(needles, strings.ToLower(t))
		}
	}
	match := func(f string) bool {
		f = strings.ToLower(f)
		for _, n := range needles {
			if f != "" && strings.Contains(f, n) {
				return true
			}
		}
		return false
	}
	out := []Entry{}
	for _, e := range s.byIDOrder() {
		if match(e.Name) || match(e.Version) || match(e.Author) || match(e.S3Key) {
			if out = append(out, *e); len(out) >= maxResults {
				break
			}
		}
	}
	return out
}

func adminAPIGet(w http.ResponseWriter, r *http.Request, s *SiteCtx, action string) {
	st := s.Store
	q := r.URL.Query()
	num := func(k string) int { n, _ := strconv.Atoi(q.Get(k)); return n }
	var res any
	var err error
	switch action {
	case "entry":
		res, err = st.APIEntry(num("id"))
	case "list":
		res, err = st.APIList(num("folder"))
	case "search":
		n := num("max")
		if n <= 0 || n > 1000 {
			n = 100
		}
		res = orderedMap{{"entries", st.APISearch(q.Get("q"), n)}}
	case "source-url":
		var u string
		if u, err = st.SourceURL(num("id")); err == nil {
			res = map[string]string{"url": u}
		}
	default:
		writeJSON(w, 404, map[string]string{"error": "unknown action " + action})
		return
	}
	writeAPIResult(w, action, res, err)
}

// ---- replacing a file's content ---------------------------------------------------------------------

func backupKey(kind string, e *Entry) string {
	base := e.S3Key[strings.LastIndex(e.S3Key, "/")+1:]
	return fmt.Sprintf("backups/%s/%s-%s", kind, time.Now().UTC().Format("20060102-150405"), base)
}

// backupObject copies a file's object under backups/<kind>/ (ignored by sync). Returns the backup key.
func (s *SiteStore) backupObject(kind string, e *Entry) (string, error) {
	rel := backupKey(kind, e)
	if err := s3Copy(s.objectKey(e.S3Key), s.objectKey(rel)); err != nil {
		return "", fmt.Errorf("backup of %s failed: %w", e.S3Key, err)
	}
	return rel, nil
}

func (s *SiteStore) StartReplace(id int, fileName string) (orderedMap, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	e, err := s.entryCopy(id)
	if err != nil {
		return nil, err
	}
	if !e.IsFile() {
		return nil, adminErr("%d is a folder - only files can be replaced", id)
	}
	if fileName != "" {
		if t := parseFileName(fileName).Type; t != e.Type {
			return nil, adminErr("%d is a .%s file and the new file is .%s - upload it as a new entry instead", id, e.Type, t)
		}
	}
	backup := ""
	if s3Exists(s.objectKey(e.S3Key)) {
		if backup, err = s.backupObject("replaced", &e); err != nil {
			return nil, err
		}
	}
	contentType := firstNonEmpty(getTypeInfo(e.Type).Mime, "application/octet-stream")
	u, err := s3PresignPut(s.objectKey(e.S3Key), contentType)
	if err != nil {
		return nil, err
	}
	return orderedMap{{"id", id}, {"key", e.S3Key}, {"contentType", contentType}, {"url", u}, {"backup", nullStr(backup)}}, nil
}

// CommitReplace saves the new size (read from the bucket, not trusted from the client). With numPages it also
// records the page count, and with clearPreviews it drops the preview list (the old images no longer match).
func (s *SiteStore) CommitReplace(id int, b body) (*Entry, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	e, err := s.entryCopy(id)
	if err != nil {
		return nil, err
	}
	if !e.IsFile() {
		return nil, adminErr("%d is a folder", id)
	}
	size, err := s3Size(s.objectKey(e.S3Key))
	if err != nil {
		return nil, adminErr("the file is not in the bucket: %v", err)
	}
	next := e
	next.SizeBytes = size
	next.MissingSince = ""
	if n := b.intOr0("numPages"); n > 0 {
		next.NumPages = n
	}
	if b.truthy("clearPreviews") {
		next.PreviewPages = []int{}
	}
	return &next, s.saveAndReload(s.touch(&next))
}

// apiDelete backs a file up before deleting it.
func (s *SiteStore) apiDelete(id int) (orderedMap, error) {
	e, err := s.entryCopy(id)
	if err != nil {
		return nil, err
	}
	backup := ""
	if e.IsFile() && s3Exists(s.objectKey(e.S3Key)) {
		if backup, err = s.backupObject("deleted", &e); err != nil {
			return nil, err
		}
	}
	if err := s.DeleteEntry(id); err != nil {
		return nil, err
	}
	return orderedMap{{"ok", true}, {"backup", nullStr(backup)}}, nil
}

// ---- bucket helpers ------------------------------------------------------------------------------

func s3Copy(fromKey, toKey string) error {
	ctx, cancel := s3ctx()
	defer cancel()
	_, err := s3().CopyObject(ctx, minio.CopyDestOptions{Bucket: bucketFor(toKey), Object: toKey}, minio.CopySrcOptions{Bucket: bucketFor(fromKey), Object: fromKey})
	return err
}

func s3Size(key string) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	info, err := s3().StatObject(ctx, bucketFor(key), key, minio.StatObjectOptions{})
	if err != nil {
		return 0, err
	}
	return info.Size, nil
}

func logAPI(s *SiteCtx, r *http.Request, action string) {
	if s.APIClient != "" && r.Method != http.MethodGet {
		log.Printf("api %s [%s] %s %s", s.ID, s.APIClient, action, clientKey(r))
	}
}
