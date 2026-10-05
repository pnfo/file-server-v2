// File server for tipitaka.lk/library and tipitaka.lk/cloud: listings, file pages with SigV2-signed download
// links, download counts, search and the admin. One process serves both sites; each has its own SQLite file.
// Port of the Astro/Node server (docs/go-rewrite.md); the HTML, URLs and data are unchanged.
//
//	file_server [serve]                                   HOST/PORT from the environment (default 127.0.0.1:4321)
//	file_server sync <library|cloud> [--dry-run] [--counts file]
//	file_server backup                                    nightly cron: snapshot each DB to <site>/backups/
//	file_server snapshot <from.db> <to.db>                consistent copy of a live database
package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func logf(format string, a ...any) { log.Printf(format, a...) }

func main() {
	log.SetFlags(0)
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		serve()
	case "sync":
		syncCmd(args)
	case "backup":
		backupCmd()
	case "snapshot":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: file_server snapshot <from.db> <to.db>")
			os.Exit(2)
		}
		db, err := openDBFile(args[0])
		if err == nil {
			err = db.Backup(args[1])
		}
		if err != nil {
			log.Fatal(err)
		}
	default:
		fmt.Fprintf(os.Stderr, "usage: file_server [serve | sync <%s> [--dry-run] [--counts file] | backup | snapshot <from.db> <to.db>]\n", strings.Join(siteIDs, "|"))
		os.Exit(2)
	}
}

func serve() {
	log.SetFlags(log.LstdFlags)
	loadTemplates()
	loadAssets()
	for _, id := range siteIDs {
		getStore(id) // load now, not on the first request
	}
	host, port := os.Getenv("HOST"), os.Getenv("PORT")
	if host == "" {
		host = "localhost"
	}
	if port == "" {
		port = "4321"
	}
	addr := net.JoinHostPort(host, port)
	srv := &http.Server{Addr: addr, Handler: newHandler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second}
	log.Printf("file server listening on http://%s (root %s, assets %s)", addr, appRoot, distDir())
	log.Fatal(srv.ListenAndServe())
}

func syncCmd(args []string) {
	var site, countsFile string
	dryRun := false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--dry-run":
			dryRun = true
		case a == "--counts" && i+1 < len(args):
			countsFile = args[i+1]
			i++
		case isSiteID(a):
			site = a
		}
	}
	if site == "" {
		fmt.Fprintf(os.Stderr, "usage: file_server sync <%s> [--dry-run] [--counts file]\n", strings.Join(siteIDs, "|"))
		os.Exit(1)
	}
	st := getStore(site)
	var counts CountInfo
	if countsFile != "" {
		var err error
		if counts, err = readCountsFile(countsFile); err != nil {
			log.Fatal(err)
		}
	}
	r, err := syncFromS3(st, dryRun, counts)
	if err != nil {
		log.Fatal(err)
	}
	prefix := ""
	if r.DryRun {
		prefix = "DRY RUN - "
	}
	fmt.Printf("[%s] %sobjects %d, files %d, folders %d\n", site, prefix, r.Objects, r.Files, r.Folders)
	fmt.Printf("added %d, updated %d, restored %d, missing %d, skipped %d\n", len(r.Added), len(r.Updated), len(r.Restored), len(r.Missing), len(r.Skipped))
	for _, s := range r.Skipped[:min(50, len(r.Skipped))] {
		fmt.Printf("  skipped: %s - %s\n", s.Key, s.Reason)
	}
	if len(r.Missing) > 0 {
		ids := make([]string, 0, 50)
		for _, id := range r.Missing[:min(50, len(r.Missing))] {
			ids = append(ids, fmt.Sprint(id))
		}
		fmt.Printf("  missing ids: %s\n", strings.Join(ids, ", "))
	}
}

// backupCmd snapshots each site DB, uploads it to <site>/backups/ (private) and keeps the last 14 locally.
func backupCmd() {
	stamp := time.Now().UTC().Format("2006-01-02")
	dir := filepath.Join(appRoot, "data", "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatal(err)
	}
	for _, site := range siteIDs {
		db, err := openSiteDB(site)
		if err != nil {
			log.Fatal(err)
		}
		file := filepath.Join(dir, fmt.Sprintf("%s-%s.db", site, stamp))
		if err := db.Backup(file); err != nil {
			log.Fatalf("[%s] backup: %v", site, err)
		}
		db.Close()
		b, err := os.ReadFile(file)
		if err != nil {
			log.Fatal(err)
		}
		if err := s3PutObject(fmt.Sprintf("%s/backups/%s-%s.db", getSiteConfig(site).S3RootFolder, site, stamp), b, "application/vnd.sqlite3"); err != nil {
			log.Fatalf("[%s] upload: %v", site, err)
		}
		entries, _ := os.ReadDir(dir)
		var old []string
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), site+"-") {
				old = append(old, e.Name())
			}
		}
		sort.Strings(old)
		for _, f := range old[:max(0, len(old)-14)] {
			os.Remove(filepath.Join(dir, f))
		}
		fmt.Printf("backed up %s -> %s\n", site, file)
	}
}
