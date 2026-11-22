package main

// Verifies a download link of each site really serves bytes (Range 0-0 -> 206) and still does when a webview
// appends a param like fbclid: the Worker for signed sites, the public bucket domain for the others.

import (
	"bufio"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SignerCheck struct {
	OK         bool   `json:"ok"`
	Status     int    `json:"status,omitempty"`
	WithFbclid int    `json:"withFbclid,omitempty"`
	Error      string `json:"error,omitempty"`
}

var (
	signerMu     sync.Mutex
	signerCached = map[string]struct {
		at     time.Time
		result SignerCheck
	}{}
)

func checkSigner(s *SiteStore) SignerCheck {
	signerMu.Lock()
	defer signerMu.Unlock()
	if c, ok := signerCached[s.Site]; ok && time.Since(c.at) < 10*time.Minute {
		return c.result
	}
	var sample *Entry
	s.mu.RLock()
	for _, e := range s.byIDOrder() {
		if e.IsFile() && e.MissingSince == "" {
			sample = e
			break
		}
	}
	var link SignedLink
	if sample != nil {
		link = s.Link(sample, "inline")
	}
	s.mu.RUnlock()
	var result SignerCheck
	if sample == nil {
		result = SignerCheck{OK: true, Error: "no files to test"}
	} else {
		client := &http.Client{Timeout: 15 * time.Second}
		get := func(u string) (int, error) {
			req, _ := http.NewRequest(http.MethodGet, u, nil)
			req.Header.Set("Range", "bytes=0-0")
			res, err := client.Do(req)
			if err != nil {
				return 0, err
			}
			res.Body.Close()
			return res.StatusCode, nil
		}
		var wg sync.WaitGroup
		var status, withFbclid int
		var err1, err2 error
		wg.Add(2)
		go func() { defer wg.Done(); status, err1 = get(link.URL) }()
		sep := "?"
		if strings.Contains(link.URL, "?") {
			sep = "&"
		}
		go func() { defer wg.Done(); withFbclid, err2 = get(link.URL + sep + "fbclid=selfcheck") }()
		wg.Wait()
		if err1 != nil || err2 != nil {
			e := err1
			if e == nil {
				e = err2
			}
			result = SignerCheck{OK: false, Error: e.Error()}
		} else {
			result = SignerCheck{OK: status == 206 && withFbclid == 206, Status: status, WithFbclid: withFbclid}
		}
	}
	if !result.OK {
		logf("SIGNER SELF-CHECK FAILED %+v", result)
	}
	signerCached[s.Site] = struct {
		at     time.Time
		result SignerCheck
	}{time.Now(), result}
	return result
}

// rssMB is the resident memory: VmRSS on Linux, the Go runtime's total elsewhere.
func rssMB() int {
	if f, err := os.Open("/proc/self/status"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if rest, ok := strings.CutPrefix(sc.Text(), "VmRSS:"); ok {
				kb, _ := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "kB")))
				return (kb + 512) / 1024
			}
		}
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int(m.Sys >> 20)
}
