package main

// Admin session: an HMAC-signed "exp.sig" cookie, the same format as the Node server's (sessions survive the switch).

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie = "fs_admin"
	sessionTTL    = 7 * 86400
)

func sessionMAC(payload string) string {
	m := hmac.New(sha256.New, []byte(getSecrets().SessionSecret))
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func createSession() string {
	exp := strconv.FormatInt(time.Now().Unix()+sessionTTL, 10)
	return exp + "." + sessionMAC(exp)
}

func verifySession(value string) bool {
	exp, sig, ok := strings.Cut(value, ".")
	if !ok || sig == "" {
		return false
	}
	n, err := strconv.ParseFloat(exp, 64)
	return err == nil && n > float64(time.Now().Unix()) && hmac.Equal([]byte(sig), []byte(sessionMAC(exp)))
}

func checkPassword(pw string) bool {
	return hmac.Equal([]byte(sessionMAC(pw)), []byte(sessionMAC(getSecrets().AdminPassword)))
}

// naive per-IP limiter for login attempts
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
