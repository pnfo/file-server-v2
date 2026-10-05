package main

// S3 is the source of truth for which files exist, where they sit and their name/version/id/type.
// SQLite keeps everything else (downloads, dates, metadata, previews, published flag). See design §8.2.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type SkippedKey struct {
	Key    string `json:"key"`
	Reason string `json:"reason"`
}

type SyncReport struct {
	Objects  int          `json:"objects"`
	Files    int          `json:"files"`
	Folders  int          `json:"folders"`
	Added    []int        `json:"added"`
	Updated  []int        `json:"updated"`
	Missing  []int        `json:"missing"`
	Restored []int        `json:"restored"`
	Skipped  []SkippedKey `json:"skipped"`
	DryRun   bool         `json:"dryRun"`
}

type CountInfo map[string]struct {
	Downloads int    `json:"downloads"`
	DateAdded string `json:"dateAdded"`
}

func readCountsFile(file string) (CountInfo, error) {
	p := file
	if !filepath.IsAbs(p) {
		p = filepath.Join(appRoot, file)
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return CountInfo{}, nil
	} else if err != nil {
		return nil, err
	}
	c := CountInfo{}
	return c, json.Unmarshal(b, &c)
}

// syncSignature is the fields owned by S3; folder sizes are computed in the store, so they never count as a change.
func syncSignature(e *Entry) string {
	size := int64(0)
	if e.IsFile() {
		size = e.SizeBytes
	}
	return fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d", e.ParentID, e.Kind, e.Type, e.Name, e.Version, e.S3Key, size)
}

type listedFile struct {
	key, name, version, typ string
	parentID                int
	size                    int64
	date                    string
}
type listedFolder struct {
	key, name, version string
	parentID           int
}

var imageRootRe = regexp.MustCompile(`^(thumbs|previews|backups)$`)

// entriesFromListing is the pure part of the sync: a bucket listing turned into entries (no I/O), for testing.
// fileOrder/folderOrder keep the listing order (the Node server's Maps iterated in insertion order).
func entriesFromListing(objects []S3Object, rootPrefix string) (files map[int]listedFile, fileOrder []int, folders map[int]listedFolder, folderOrder []int, skipped []SkippedKey) {
	files, folders, skipped = map[int]listedFile{}, map[int]listedFolder{}, []SkippedKey{}
	for _, o := range objects {
		rel := o.Key[min(len(o.Key), len(rootPrefix)+1):]
		parts := strings.Split(rel, "/")
		fileName := parts[len(parts)-1]
		parts = parts[:len(parts)-1]
		// root-level keys like thumbs/ and previews/ hold images, not entries
		if len(parts) > 0 && imageRootRe.MatchString(parts[0]) {
			continue
		}
		f := parseFileName(fileName)
		if f.ID == 0 {
			skipped = append(skipped, SkippedKey{rel, "no {id} in file name"})
			continue
		}
		if f.Type == "" {
			skipped = append(skipped, SkippedKey{rel, "no file extension"})
			continue
		}
		parentID, ok := 0, true
		for i := range parts {
			seg := parseFileName(parts[i])
			if seg.ID == 0 { // "-dev"-style segment: everything below is unlisted
				parentID = -1
				continue
			}
			key := strings.Join(parts[:i+1], "/")
			existing, has := folders[seg.ID]
			if has && existing.key != key {
				skipped = append(skipped, SkippedKey{rel, fmt.Sprintf(`folder id %d used by "%s" and "%s"`, seg.ID, existing.key, key)})
				ok = false
				break
			}
			if _, isFile := files[seg.ID]; isFile {
				skipped = append(skipped, SkippedKey{rel, fmt.Sprintf("folder id %d is also a file id", seg.ID)})
				ok = false
				break
			}
			if !has {
				folders[seg.ID] = listedFolder{key, seg.Name, seg.Version, parentID}
				folderOrder = append(folderOrder, seg.ID)
			}
			parentID = seg.ID
		}
		if !ok {
			continue
		}
		if dup, has := files[f.ID]; has {
			skipped = append(skipped, SkippedKey{rel, fmt.Sprintf(`id %d already used by "%s"`, f.ID, dup.key)})
			continue
		}
		if dup, has := folders[f.ID]; has {
			skipped = append(skipped, SkippedKey{rel, fmt.Sprintf(`id %d already used by "%s"`, f.ID, dup.key)})
			continue
		}
		date := ""
		if !o.LastModified.IsZero() {
			date = o.LastModified.UTC().Format("2006-01-02")
		}
		files[f.ID] = listedFile{rel, f.Name, f.Version, f.Type, parentID, o.Size, date}
		fileOrder = append(fileOrder, f.ID)
	}
	return
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

func syncFromS3(s *SiteStore, dryRun bool, counts CountInfo) (*SyncReport, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	root := s.Cfg.S3RootFolder
	objects, err := s3ListAll(root + "/")
	if err != nil {
		return nil, err
	}
	files, fileOrder, folders, folderOrder, skipped := entriesFromListing(objects, root)
	if counts == nil {
		counts = CountInfo{}
	}
	now := nowISO()
	report := &SyncReport{Objects: len(objects), Files: len(files), Folders: len(folders), Added: []int{}, Updated: []int{},
		Missing: []int{}, Restored: []int{}, Skipped: skipped, DryRun: dryRun}
	var changed []*Entry

	s.mu.RLock()
	existing := map[int]Entry{}
	for _, e := range s.byID {
		existing[e.ID] = *e
	}
	s.mu.RUnlock()

	apply := func(id int, patch func(e *Entry), dateAdded string) {
		old, has := existing[id]
		var e Entry
		if has {
			e = old
		} else {
			c := counts[strconv.Itoa(id)]
			e = Entry{ID: id, Kind: "file", IsPublished: true, UpdatedAt: now, Downloads: c.Downloads,
				DateAdded: firstNonEmpty(c.DateAdded, dateAdded, today())}
		}
		before := syncSignature(&e)
		patch(&e)
		e.MissingSince = ""
		switch {
		case !has:
			report.Added = append(report.Added, id)
		case old.MissingSince != "":
			report.Restored = append(report.Restored, id)
		case before != syncSignature(&e):
			report.Updated = append(report.Updated, id)
		default:
			return
		}
		// a newly imported entry hasn't been changed yet: "updated" starts as the date it was added
		if has {
			e.UpdatedAt = now
		} else {
			e.UpdatedAt = e.DateAdded
		}
		changed = append(changed, &e)
	}

	// a new folder takes the oldest date of the files below it
	folderDate := map[int]string{}
	for _, id := range fileOrder {
		f := files[id]
		d := firstNonEmpty(counts[strconv.Itoa(id)].DateAdded, f.date, today())
		pid := f.parentID
		for p, ok, guard := folders[pid], hasKey(folders, pid), 0; ok && guard < 50; guard++ {
			if cur, has := folderDate[pid]; !has || d < cur {
				folderDate[pid] = d
			}
			pid = p.parentID
			p, ok = folders[pid], hasKey(folders, pid)
		}
	}
	for _, id := range folderOrder {
		f := folders[id]
		apply(id, func(e *Entry) {
			e.Kind, e.Type, e.ParentID, e.Name, e.Version, e.S3Key, e.SizeBytes = "folder", "folder", f.parentID, f.name, f.version, f.key, 0
		}, folderDate[id])
	}
	for _, id := range fileOrder {
		f := files[id]
		apply(id, func(e *Entry) {
			e.Kind, e.Type, e.ParentID, e.Name, e.Version, e.S3Key, e.SizeBytes = "file", f.typ, f.parentID, f.name, f.version, f.key, f.size
		}, f.date)
	}

	// files that vanished from the bucket are kept (with their counts) but hidden until they reappear
	ids := make([]int, 0, len(existing))
	for id := range existing {
		ids = append(ids, id)
	}
	sortInts(ids)
	for _, id := range ids {
		e := existing[id]
		if _, listed := files[id]; e.IsFile() && !listed && e.MissingSince == "" {
			report.Missing = append(report.Missing, id)
			e.MissingSince, e.UpdatedAt = today(), now
			changed = append(changed, &e)
		}
	}
	if !dryRun {
		if err := s.DB.SaveMany(changed); err != nil {
			return nil, err
		}
		if err := s.DB.SetMeta("last_sync", now); err != nil {
			return nil, err
		}
		if err := s.reload(); err != nil {
			return nil, err
		}
	}
	return report, nil
}

func hasKey[K comparable, V any](m map[K]V, k K) bool { _, ok := m[k]; return ok }
