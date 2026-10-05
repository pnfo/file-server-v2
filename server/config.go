package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var siteIDs = []string{"library", "cloud"}

const (
	bucket     = "tipitaka"
	s3Endpoint = "sgp1.digitaloceanspaces.com"
	cdnHost    = "tipitaka.sgp1.cdn.digitaloceanspaces.com"
)

type SiteConfig struct {
	ID             string `json:"-"`
	FileTypeName   string `json:"fileTypeName"`
	RootFolderName string `json:"rootFolderName"`
	WebURLRoot     string `json:"webUrlRoot"`
	S3RootFolder   string `json:"s3RootFolder"`
	OgImage        string `json:"ogImage"`
	DisplayThumbs  bool   `json:"displayThumbs"`
	IDToInfoFile   string `json:"idToInfoFile"`
}

type Keys struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
}

type Secrets struct {
	AdminPassword string `json:"adminPassword"`
	SessionSecret string `json:"sessionSecret"`
	S3            Keys   `json:"s3"`
	Signer        *Keys  `json:"signer"`
}

// appRoot holds config/ and data/ (APP_ROOT, default the working directory).
var appRoot = func() string {
	if r := os.Getenv("APP_ROOT"); r != "" {
		return r
	}
	wd, _ := os.Getwd()
	return wd
}()

// distDir holds the built assets: <root>/dist on the server, server/dist in the repo (where dist/ is Astro's).
func distDir() string {
	if d := os.Getenv("DIST_DIR"); d != "" {
		return d
	}
	if _, err := os.Stat(filepath.Join(appRoot, "dist", "manifest.json")); err == nil {
		return filepath.Join(appRoot, "dist")
	}
	return filepath.Join(appRoot, "server", "dist")
}

func readJSON(file string, v any) error {
	b, err := os.ReadFile(filepath.Join(appRoot, file))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

var (
	siteConfigs   = map[string]*SiteConfig{}
	siteConfigsMu sync.Mutex
)

func getSiteConfig(id string) *SiteConfig {
	siteConfigsMu.Lock()
	defer siteConfigsMu.Unlock()
	if c := siteConfigs[id]; c != nil {
		return c
	}
	c := &SiteConfig{}
	if err := readJSON(fmt.Sprintf("config/%s.json", id), c); err != nil {
		log.Fatalf("config/%s.json: %v", id, err)
	}
	c.ID = id
	siteConfigs[id] = c
	return c
}

var getSecrets = sync.OnceValue(func() *Secrets {
	s := &Secrets{}
	if err := readJSON("config/secrets.json", s); err != nil {
		log.Fatalf("config/secrets.json: %v", err)
	}
	if s.Signer == nil {
		log.Print(`config/secrets.json has no "signer" key - signing download links with the read/write key. Create a read-only Spaces key.`)
		s.Signer = &s.S3
	}
	return s
})

type prefixInfo struct {
	site    string
	isAlias bool
}

// URL prefixes that map to a site. SITE_ALIASES=library-v2:library,cloud-v2:cloud lets this server run side by
// side with production during testing; alias pages are marked noindex.
var prefixes, prefixOrder = func() (map[string]prefixInfo, []string) {
	m := map[string]prefixInfo{}
	var order []string
	for _, id := range siteIDs {
		m[id] = prefixInfo{id, false}
		order = append(order, id)
	}
	for _, pair := range strings.Split(os.Getenv("SITE_ALIASES"), ",") {
		prefix, site, ok := strings.Cut(pair, ":")
		prefix, site = strings.TrimSpace(prefix), strings.TrimSpace(site)
		if ok && isSiteID(site) {
			m[prefix] = prefixInfo{site, true}
			order = append(order, prefix)
		}
	}
	return m, order
}()

func isSiteID(s string) bool {
	for _, id := range siteIDs {
		if id == s {
			return true
		}
	}
	return false
}

// prefixFor is the URL prefix of site on the same deployment flavour (production or test alias).
func prefixFor(site string, isAlias bool) string {
	for _, p := range prefixOrder {
		if r := prefixes[p]; r.site == site && r.isAlias == isAlias {
			return p
		}
	}
	return site
}
