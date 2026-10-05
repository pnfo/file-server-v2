package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure Go: cross-compiles from the Mac, no glibc dependency on the droplet
)

// Entry is a file or folder. Empty strings and zero numbers stand for NULL in the database.
type Entry struct {
	ID             int
	ParentID       int    // 0 = root, -1 = unlisted (inside a folder segment without an {id})
	Kind           string // folder | file
	Type           string // pdf, mp3, ... ("folder" for folders)
	Name           string
	Version        string // the [version] tag in the file name
	Author         string
	PublishingYear int
	PrintedYear    int
	Description    string
	S3Key          string // relative to the site root folder in the bucket
	SizeBytes      int64  // folders: sum of published descendant files (computed in the store)
	NumEntries     int    // folders: count of published descendant files (computed in the store)
	NumPages       int
	PreviewPages   []int // nil = legacy thumbs/{id}-{0,1}.jpg
	Downloads      int
	IsPublished    bool
	DateAdded      string // YYYY-MM-DD
	UpdatedAt      string
	MissingSince   string
	sortKey        []byte // collation key of Name (store.reload)
}

func (e *Entry) IsFolder() bool { return e.Kind == "folder" }
func (e *Entry) IsFile() bool   { return e.Kind == "file" }

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

// MarshalJSON keeps the shape the admin script got from the Node server (nulls included).
func (e *Entry) MarshalJSON() ([]byte, error) {
	var pages any
	if e.PreviewPages != nil {
		pages = e.PreviewPages
	}
	return marshalJSON(orderedMap{
		{"id", e.ID}, {"parentId", e.ParentID}, {"kind", e.Kind}, {"type", e.Type}, {"name", e.Name},
		{"version", nullStr(e.Version)}, {"author", nullStr(e.Author)}, {"publishingYear", nullInt(e.PublishingYear)},
		{"printedYear", nullInt(e.PrintedYear)}, {"description", nullStr(e.Description)}, {"s3Key", e.S3Key},
		{"sizeBytes", e.SizeBytes}, {"numEntries", e.NumEntries}, {"numPages", nullInt(e.NumPages)},
		{"previewPages", pages}, {"downloads", e.Downloads}, {"isPublished", e.IsPublished},
		{"dateAdded", e.DateAdded}, {"updatedAt", e.UpdatedAt}, {"missingSince", nullStr(e.MissingSince)},
	})
}

// Each item runs once, in order, tracked by PRAGMA user_version (same list as the Node server's db.ts).
var migrations = []string{
	`CREATE TABLE entries (
    id              INTEGER PRIMARY KEY,
    parent_id       INTEGER NOT NULL DEFAULT 0,
    kind            TEXT NOT NULL CHECK (kind IN ('folder', 'file')),
    type            TEXT NOT NULL,
    name            TEXT NOT NULL,
    version         TEXT,
    author          TEXT,
    publishing_year INTEGER,
    printed_year    INTEGER,
    description     TEXT,
    s3_key          TEXT NOT NULL,
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    num_pages       INTEGER,
    preview_pages   TEXT,
    downloads       INTEGER NOT NULL DEFAULT 0,
    is_published    INTEGER NOT NULL DEFAULT 1,
    date_added      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    missing_since   TEXT
  );
  CREATE INDEX entries_parent ON entries(parent_id);
  CREATE TABLE meta (k TEXT PRIMARY KEY, v TEXT);`,
}

func today() string { return time.Now().UTC().Format("2006-01-02") }

// nowISO matches JS new Date().toISOString().
func nowISO() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000Z") }

type SiteDB struct{ db *sql.DB }

const entryCols = "id, parent_id, kind, type, name, version, author, publishing_year, printed_year, description, " +
	"s3_key, size_bytes, num_pages, preview_pages, downloads, is_published, date_added, updated_at, missing_since"

func openSiteDB(site string) (*SiteDB, error) {
	return openDBFile(filepath.Join(appRoot, "data", site+".db"))
}

func openDBFile(file string) (*SiteDB, error) {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+file+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // reads come from memory; one connection keeps writes simple and serial
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return nil, err
	}
	for v := version; v < len(migrations); v++ {
		tx, err := db.Begin()
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(migrations[v]); err != nil {
			tx.Rollback()
			return nil, err
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}
	return &SiteDB{db}, nil
}

func scanEntry(rows interface{ Scan(...any) error }) (*Entry, error) {
	var e Entry
	var version, author, description, previewPages, missingSince sql.NullString
	var publishingYear, printedYear, numPages sql.NullInt64
	var isPublished int
	if err := rows.Scan(&e.ID, &e.ParentID, &e.Kind, &e.Type, &e.Name, &version, &author, &publishingYear, &printedYear,
		&description, &e.S3Key, &e.SizeBytes, &numPages, &previewPages, &e.Downloads, &isPublished, &e.DateAdded,
		&e.UpdatedAt, &missingSince); err != nil {
		return nil, err
	}
	e.Version, e.Author, e.Description, e.MissingSince = version.String, author.String, description.String, missingSince.String
	e.PublishingYear, e.PrintedYear, e.NumPages = int(publishingYear.Int64), int(printedYear.Int64), int(numPages.Int64)
	e.IsPublished = isPublished != 0
	if previewPages.Valid && previewPages.String != "" {
		if err := json.Unmarshal([]byte(previewPages.String), &e.PreviewPages); err != nil {
			return nil, fmt.Errorf("entry %d preview_pages: %w", e.ID, err)
		}
	}
	return &e, nil
}

func (d *SiteDB) All() ([]*Entry, error) {
	rows, err := d.db.Query("SELECT " + entryCols + " FROM entries ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

func saveEntry(x execer, e *Entry) error {
	var pages any
	if len(e.PreviewPages) > 0 {
		b, _ := json.Marshal(e.PreviewPages)
		pages = string(b)
	}
	size := e.SizeBytes
	if !e.IsFile() {
		size = 0
	}
	published := 0
	if e.IsPublished {
		published = 1
	}
	cols := strings.Split(entryCols, ", ")
	set := make([]string, 0, len(cols)-1)
	for _, c := range cols[1:] {
		set = append(set, c+" = excluded."+c)
	}
	_, err := x.Exec("INSERT INTO entries ("+entryCols+") VALUES (?"+strings.Repeat(", ?", len(cols)-1)+") ON CONFLICT(id) DO UPDATE SET "+strings.Join(set, ", "),
		e.ID, e.ParentID, e.Kind, e.Type, e.Name, nullStr(e.Version), nullStr(e.Author), nullInt(e.PublishingYear),
		nullInt(e.PrintedYear), nullStr(e.Description), e.S3Key, size, nullInt(e.NumPages), pages, e.Downloads,
		published, e.DateAdded, e.UpdatedAt, nullStr(e.MissingSince))
	return err
}

func (d *SiteDB) Save(e *Entry) error { return saveEntry(d.db, e) }

func (d *SiteDB) SaveMany(list []*Entry) error {
	tx, err := d.db.Begin()
	if err != nil {
		return err
	}
	for _, e := range list {
		if err := saveEntry(tx, e); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (d *SiteDB) Delete(id int) error {
	_, err := d.db.Exec("DELETE FROM entries WHERE id = ?", id)
	return err
}

func (d *SiteDB) Hit(id int) error {
	_, err := d.db.Exec("UPDATE entries SET downloads = downloads + 1 WHERE id = ?", id)
	return err
}

func (d *SiteDB) GetMeta(k string) string {
	var v sql.NullString
	d.db.QueryRow("SELECT v FROM meta WHERE k = ?", k).Scan(&v)
	return v.String
}

func (d *SiteDB) SetMeta(k, v string) error {
	_, err := d.db.Exec("INSERT INTO meta (k, v) VALUES (?, ?) ON CONFLICT(k) DO UPDATE SET v = excluded.v", k, v)
	return err
}

// Backup writes a consistent copy of the database to dest.
func (d *SiteDB) Backup(dest string) error {
	os.Remove(dest)
	_, err := d.db.Exec("VACUUM INTO ?", dest)
	return err
}

func (d *SiteDB) Close() error { return d.db.Close() }
