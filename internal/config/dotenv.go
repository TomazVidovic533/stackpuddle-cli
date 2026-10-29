package config

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/TomazVidovic533/stackpuddle-cli/internal/diagnostics"
	"github.com/TomazVidovic533/stackpuddle/model"
)

// CodeDotenvInvalid is the code of a dotenv parse error. It names file, line and key, never the line.
const CodeDotenvInvalid = "config.dotenv_invalid"

// maxDotenvFindings bounds the report of one file so a hostile file cannot flood the output.
const maxDotenvFindings = 20

// DotenvEntry is one supplied key of a dotenv file.
type DotenvEntry struct {
	// Value is the supplied text; an empty Value is a supplied empty value, not a missing one.
	Value string
	// Line is the 1-based line that supplied it.
	Line int
}

// DotenvFile is a parsed dotenv file. The zero value holds no keys.
type DotenvFile struct {
	entries map[string]DotenvEntry
}

// Lookup returns the entry for key; ok is false when the file does not supply it.
func (f DotenvFile) Lookup(key string) (DotenvEntry, bool) {
	e, ok := f.entries[key]
	return e, ok
}

// dotenvReason is a closed set of parse failures; its text never contains input.
type dotenvReason int

const (
	reasonMissingEquals dotenvReason = iota + 1
	reasonInvalidKey
	reasonUnterminatedQuote
	reasonTrailingAfterQuote
	reasonUnsupportedEscape
	reasonDuplicateKey
	reasonInvalidEncoding
)

func (r dotenvReason) String() string {
	switch r {
	case reasonMissingEquals:
		return "expected KEY=value"
	case reasonInvalidKey:
		return "invalid key"
	case reasonUnterminatedQuote:
		return "unterminated quote"
	case reasonTrailingAfterQuote:
		return "characters after the closing quote"
	case reasonUnsupportedEscape:
		return "unsupported escape"
	case reasonDuplicateKey:
		return "duplicate key"
	case reasonInvalidEncoding:
		return "invalid UTF-8"
	}
	return "invalid"
}

// ParseDotenv parses data, the content of the dotenv file named file, in the supported subset: KEY=value
// lines, # comments, blank lines, single- or double-quoted values without interpolation and with no
// escape beyond \" and \\ (double quotes only), no export prefix, no multi-line values. A duplicate key
// is refused. Each finding names file, line and, when the key is a valid name, the key, never the text
// of the line. On any finding the returned file holds no keys.
func ParseDotenv(file string, data []byte) (DotenvFile, []diagnostics.Finding) {
	var findings []diagnostics.Finding
	entries := map[string]DotenvEntry{}
	fail := func(line int, key string, why dotenvReason) {
		if len(findings) >= maxDotenvFindings {
			return
		}
		msg := fmt.Sprintf("dotenv line %d: %s", line, why)
		if key != "" {
			msg += fmt.Sprintf(" for key %q", key)
		}
		findings = append(findings, diagnostics.Finding{
			Code: CodeDotenvInvalid, Location: model.NewLocation(file, "", line, 0), Message: msg,
		})
	}
	if !utf8.Valid(data) {
		fail(1, "", reasonInvalidEncoding)
		return DotenvFile{}, findings
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	for i, raw := range strings.Split(string(data), "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rest, found := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !found {
			fail(lineNo, "", reasonMissingEquals)
			continue
		}
		if !validDotenvKey(key) {
			fail(lineNo, "", reasonInvalidKey)
			continue
		}
		value, why, ok := parseDotenvValue(strings.TrimSpace(rest))
		if !ok {
			fail(lineNo, key, why)
			continue
		}
		if _, dup := entries[key]; dup {
			fail(lineNo, key, reasonDuplicateKey)
			continue
		}
		entries[key] = DotenvEntry{Value: value, Line: lineNo}
	}
	if len(findings) > 0 {
		return DotenvFile{}, findings
	}
	return DotenvFile{entries: entries}, nil
}

func validDotenvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		letter := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		digit := r >= '0' && r <= '9'
		if !letter && (i == 0 || !digit) {
			return false
		}
	}
	return true
}

func parseDotenvValue(s string) (string, dotenvReason, bool) {
	if s == "" {
		return "", 0, true
	}
	switch s[0] {
	case '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", reasonUnterminatedQuote, false
		}
		if strings.TrimSpace(s[end+2:]) != "" {
			return "", reasonTrailingAfterQuote, false
		}
		return s[1 : end+1], 0, true
	case '"':
		return parseDoubleQuoted(s[1:])
	}
	return s, 0, true
}

func parseDoubleQuoted(s string) (string, dotenvReason, bool) {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
			if i >= len(s) {
				return "", reasonUnterminatedQuote, false
			}
			if s[i] != '"' && s[i] != '\\' {
				return "", reasonUnsupportedEscape, false
			}
			b.WriteByte(s[i])
		case '"':
			if strings.TrimSpace(s[i+1:]) != "" {
				return "", reasonTrailingAfterQuote, false
			}
			return b.String(), 0, true
		default:
			b.WriteByte(s[i])
		}
	}
	return "", reasonUnterminatedQuote, false
}
