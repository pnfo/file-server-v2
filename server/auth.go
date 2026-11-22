package main

import (
	"sync"
	"time"
)

// naive per-IP limiter for failed API tokens
var (
	loginMu       sync.Mutex
	loginAttempts = map[string]struct {
		n     int
		until time.Time
	}{}
)

func loginAllowed(ip string) bool {
	loginMu.Lock()
	defer loginMu.Unlock()
	a, ok := loginAttempts[ip]
	return !ok || a.n < 5 || a.until.Before(time.Now())
}

func recordLogin(ip string, success bool) {
	loginMu.Lock()
	defer loginMu.Unlock()
	if success {
		delete(loginAttempts, ip)
		return
	}
	a, ok := loginAttempts[ip]
	n := 1
	if ok && a.until.After(time.Now()) {
		n = a.n + 1
	}
	loginAttempts[ip] = struct {
		n     int
		until time.Time
	}{n, time.Now().Add(15 * time.Minute)}
}
