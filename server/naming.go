package main

// Bucket naming convention, unchanged from production: name[version]{id}.type
// Folders are path segments "name{id}"; a segment without {id} makes everything below it unlisted.

import (
	"regexp"
	"strconv"
	"strings"
)

type ParsedName struct {
	Name    string
	Version string
	ID      int
	Type    string
}

var fileNameRe = regexp.MustCompile(`^(.+?)(?:\[(.*)\])?(?:\{(\d+)\})?(?:\.(\w+))?$`)

func parseFileName(fileName string) ParsedName {
	m := fileNameRe.FindStringSubmatch(fileName)
	if m == nil {
		return ParsedName{Name: fileName}
	}
	id, _ := strconv.Atoi(m[3])
	return ParsedName{Name: jsTrim(m[1]), Version: jsTrim(m[2]), ID: id, Type: strings.ToLower(m[4])}
}

var (
	cleanCharsRe = regexp.MustCompile(`[\\/{}\[\]]`)
	spacesRe     = regexp.MustCompile(`[\s\x{00A0}\x{1680}\x{2000}-\x{200A}\x{2028}\x{2029}\x{202F}\x{205F}\x{3000}\x{FEFF}]+`) // JS \s
)

func cleanName(s string) string {
	return jsTrim(spacesRe.ReplaceAllString(cleanCharsRe.ReplaceAllString(s, " "), " "))
}

func buildFileName(name, version string, id int, typ string) string {
	v := ""
	if version != "" {
		v = "[" + cleanName(version) + "]"
	}
	return cleanName(name) + v + "{" + strconv.Itoa(id) + "}." + typ
}

func buildFolderName(name string, id int) string {
	return cleanName(name) + "{" + strconv.Itoa(id) + "}"
}

// saveAsName is the name the browser saves the file as: no [version]{id} noise, version kept when present.
func saveAsName(e *Entry) string {
	base := cleanName(e.Name)
	if e.Version != "" {
		base += " - " + cleanName(e.Version)
	}
	return base + "." + e.Type
}
