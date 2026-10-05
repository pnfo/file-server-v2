package main

// Small helpers that reproduce JavaScript behaviour the Node server relied on (number parsing, URI encoding,
// trimming, rounding), so pages, links and admin input handling come out the same.

import (
	"bytes"
	"encoding/json"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
)

// orderedMap marshals as a JSON object with keys in the given order.
type orderedMap []struct {
	k string
	v any
}

func (m orderedMap) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, kv := range m {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := marshalJSON(kv.k)
		v, err := marshalJSON(kv.v)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// marshalJSON is json.Marshal without HTML escaping (like JSON.stringify: signed URLs keep their & as is).
func marshalJSON(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

func isJSSpace(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }

// jsTrim is String.prototype.trim.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// sliceUTF16 is s.slice(0, n): n counts UTF-16 code units.
func sliceUTF16(s string, n int) string {
	units := 0
	for i, r := range s {
		units += utf16.RuneLen(r)
		if units > n {
			return s[:i]
		}
	}
	return s
}

// jsNumber is Number(v) for a decoded JSON value; missing values are NaN.
func jsNumber(v any, present bool) float64 {
	if !present {
		return math.NaN()
	}
	switch x := v.(type) {
	case nil:
		return 0
	case bool:
		if x {
			return 1
		}
		return 0
	case float64:
		return x
	case string:
		t := jsTrim(x)
		if t == "" {
			return 0
		}
		if strings.HasPrefix(t, "0x") || strings.HasPrefix(t, "0X") {
			if n, err := strconv.ParseUint(t[2:], 16, 64); err == nil {
				return float64(n)
			}
			return math.NaN()
		}
		switch t {
		case "Infinity", "+Infinity":
			return math.Inf(1)
		case "-Infinity":
			return math.Inf(-1)
		}
		if strings.ContainsAny(t, "_xXpPnN") { // ParseFloat accepts forms Number() does not (1_000, NaN, hex floats)
			return math.NaN()
		}
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return math.NaN()
		}
		return f
	}
	return math.NaN() // arrays and objects
}

// jsTruthy is !!v for a decoded JSON value.
func jsTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	}
	return true
}

// jsParseInt is parseInt(s, 10); ok is false for NaN.
func jsParseInt(s string) (int, bool) {
	t := strings.TrimLeftFunc(s, isJSSpace)
	neg := false
	if t != "" && (t[0] == '+' || t[0] == '-') {
		neg = t[0] == '-'
		t = t[1:]
	}
	end := 0
	for end < len(t) && t[end] >= '0' && t[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(t[:end])
	if err != nil {
		return 0, false
	}
	if neg {
		n = -n
	}
	return n, true
}

func encodeURI(s string, keep func(byte) bool) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if keep(c) {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}

func isUnreserved(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~'
}

// encodeURIComponent is the JS function of that name.
func encodeURIComponent(s string) string {
	return encodeURI(s, func(c byte) bool { return isUnreserved(c) || strings.IndexByte("!'()*", c) >= 0 })
}

// encodeStrict is encodeURIComponent plus !'()* - some media loaders (Safari/AVFoundation) mishandle those raw.
func encodeStrict(s string) string { return encodeURI(s, isUnreserved) }

// toFixed1 is Number.prototype.toFixed(1): the exact binary value, ties rounded up (Go's 'f' format rounds them to even).
func toFixed1(x float64) string {
	f := new(big.Float).SetPrec(200).SetFloat64(x)
	f.Mul(f, big.NewFloat(10))
	f.Add(f, big.NewFloat(0.5))
	n, _ := f.Int(nil) // truncates; x is positive here, so this is floor
	s := n.String()
	if len(s) < 2 {
		s = strings.Repeat("0", 2-len(s)) + s
	}
	return s[:len(s)-1] + "." + s[len(s)-1:]
}

// formatNumber puts thousands separators in an integer (String(n) with commas).
func formatNumber(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
