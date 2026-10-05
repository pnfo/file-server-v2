package main

// Sinhala sort order matching Intl.Collator('si') (ICU) in the Node server and the browser.
//
// x/text can't reproduce ICU's `si` collation directly: it has no script reordering (ICU's `si` sorts Sinhala
// before Latin) and its Sinhala tailoring gives anusvara the weight of ක ("කං" == "කක"). So this uses the root
// collation on rewritten text that encodes the `si` rules:
//   - Latin letters (after NFD, so é is e + accent) become Deseret, a cased script that sorts after Sinhala
//     with the same lower-before-upper tie-break;
//   - anusvara ං and visarga ඃ (root: before the vowels; si: after ඖ) become ඖ + a Han ideograph, which sorts
//     after every script, so they land right after ඖ and before ක;
//   - ඤ and ඥ swap (si: ඥ before ඤ).
// The result matches Intl.Collator('si') on every name in both databases and on generated strings
// (testdata/collation_si.txt). Not handled: candrabindu ඁ (ICU sorts it first), unused in the data.

import (
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

func collationText(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(s) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(0x10428 + (r - 'a'))
		case r >= 'A' && r <= 'Z':
			b.WriteRune(0x10400 + (r - 'A'))
		case r == '\u0D82': // ං
			b.WriteString("\u0D96\u4E00")
		case r == '\u0D83': // ඃ
			b.WriteString("\u0D96\u4E01")
		case r == '\u0DA4': // ඤ
			b.WriteRune('\u0DA5')
		case r == '\u0DA5': // ඥ
			b.WriteRune('\u0DA4')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Collator is not safe for concurrent use: make one per goroutine (they are cheap).
type Collator struct {
	c   *collate.Collator
	buf collate.Buffer
}

func newCollator() *Collator { return &Collator{c: collate.New(language.Und)} }

func (c *Collator) Compare(a, b string) int {
	return c.c.CompareString(collationText(a), collationText(b))
}

// Key returns a sort key: bytes.Compare on keys orders like Compare.
func (c *Collator) Key(s string) []byte {
	k := c.c.KeyFromString(&c.buf, collationText(s))
	out := append([]byte(nil), k...)
	c.buf.Reset()
	return out
}
