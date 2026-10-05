package main

// File type presentation, ported from the production stores/utils.js (icons/labels) and
// design-concerns.md concern 3 (colours). Icons are keys into icons.go.

import (
	"regexp"
	"strconv"
	"strings"
)

type TypeInfo struct {
	Icon         string
	Label        string
	DownloadText string
	ViewText     string // primary button for types the browser opens inline
	Mime         string
	Color        string // tailwind text colour class
}

func ti(icon, label, downloadText, mime, color string, viewText ...string) TypeInfo {
	v := downloadText
	if len(viewText) > 0 {
		v = viewText[0]
	}
	return TypeInfo{icon, label, downloadText, v, mime, color}
}

var typeToInfo = map[string]TypeInfo{
	"pdf": ti("pdf", "PDF", "PDF ගොනුව බාගත කරගන්න", "application/pdf", "text-rose-400", "PDF ගොනුව බලන්න"),
	"htm": ti("html", "WEB", "HTML ගොනුව බාගත කරගන්න", "text/html", "text-blue-400", "HTML ගොනුව බලන්න"),
	"col": ti("folder", "ගොනුව", "බහාලුම අරින්න", "", "text-amber-500"),
	"zip": ti("zip", "ZIP", "ZIP ගොනුව බාගත කරගන්න", "application/zip", "text-purple-400"),
	"apk": ti("android", "APP", "ඇන්ඩ්‍රොයිඩ් මෘදුකාංගය ලබාගන්න", "application/vnd.android.package-archive", "text-emerald-400"),
	"doc": ti("word", "DOC", "Word ගොනුව බාගත කරගන්න", "application/msword", "text-sky-400"),
	"xls": ti("excel", "EXCEL", "Excel වගුව බාගත කරගන්න", "application/vnd.ms-excel", "text-emerald-500"),
	"jpg": ti("image", "IMAGE", "පින්තූරය බාගත කරගන්න", "image/jpeg", "text-amber-400", "පින්තූරය බලන්න"),
	"png": ti("image", "IMAGE", "පින්තූරය බාගත කරගන්න", "image/png", "text-amber-400", "පින්තූරය බලන්න"),
	"txt": ti("text", "TXT", "TEXT ගොනුව බාගත කරගන්න", "text/plain", "text-amber-400", "TEXT ගොනුව බලන්න"),
	"mp3": ti("audio", "MP3", "ධර්ම දේශනාව බාගත කරගන්න", "audio/mpeg", "text-emerald-400"),
	"m4a": ti("audio", "M4A", "ධර්ම දේශනාව බාගත කරගන්න", "audio/mp4", "text-purple-400"),
	"unk": ti("file", "FILE", "බාගත කරගන්න", "application/octet-stream", "text-amber-400"),
}

func getTypeInfo(typ string) TypeInfo {
	raw := strings.ToLower(typ)
	if raw == "folder" || raw == "coll" {
		return typeToInfo["col"]
	}
	type3 := raw
	if strings.HasPrefix(raw, "jpe") {
		type3 = "jpg"
	} else if len(raw) > 3 {
		type3 = raw[:3]
	}
	if t, ok := typeToInfo[type3]; ok {
		return t
	}
	return typeToInfo["unk"]
}

func isAudio(typ string) bool { return typ == "mp3" || typ == "m4a" }

var viewableRe = regexp.MustCompile(`^(pdf|html?|txt|jpe?g|png)$`)

// isViewable: types a browser shows inline - these get a separate "save to device" link.
func isViewable(typ string) bool { return viewableRe.MatchString(typ) }

type SizeInfo struct{ Numeral, Unit string }

var sizeUnits = []string{"Bytes", "KB", "MB", "GB", "TB"}

func getSizeInfo(size int64) SizeInfo {
	if size <= 0 {
		return SizeInfo{"0", "KB"}
	}
	i := 0
	for i < len(sizeUnits)-1 && size >= int64(1)<<(10*(i+1)) {
		i++
	}
	n := float64(size) / float64(int64(1)<<(10*i))
	if n < 10 {
		return SizeInfo{toFixed1(n), sizeUnits[i]}
	}
	return SizeInfo{strconv.FormatInt(int64(n+0.5), 10), sizeUnits[i]} // Math.round
}

// sizeUnitColor: design-concerns.md rule 2: GB/TB red, everything else green.
func sizeUnitColor(unit string) string {
	if unit == "GB" || unit == "TB" {
		return "text-rose-500 dark:text-rose-400"
	}
	return "text-emerald-600 dark:text-emerald-400"
}

// UN-Abhaya is used for Sinhala only, in normal weight (no faux bold). The page font stack applies it to
// Sinhala characters automatically; a line that mixes Sinhala with Latin letters or digits gets the `si`
// class so the whole line is set in UN-Abhaya (e.g. "PDF ගොනුව බලන්න").
var (
	sinhalaRe      = regexp.MustCompile(`[\x{0D80}-\x{0DFF}]`)
	latinOrDigitRe = regexp.MustCompile(`[A-Za-z0-9]`)
)

func fontClass(text string) string {
	if sinhalaRe.MatchString(text) && latinOrDigitRe.MatchString(text) {
		return "si"
	}
	return ""
}
