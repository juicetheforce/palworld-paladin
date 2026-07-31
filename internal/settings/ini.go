package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// INI is a parsed PalWorldSettings.ini: the fixed header line plus the
// single OptionSettings tuple, held as an ORDERED list of raw pairs.
// Round-trip fidelity is the contract: serializing an unmodified INI
// reproduces the input byte-for-byte (minus a normalized trailing
// newline), because any formatting creativity risks the silent-reset trap
// (key-list global gotcha "silent-default-reset").
type INI struct {
	Header string // e.g. "[/Script/Pal.PalGameWorldSettings]"
	pairs  []pair // ordered
	index  map[string]int
}

type pair struct {
	key string
	raw string // value exactly as it appears in the file
}

// ParseINI parses file content. It enforces the structural rules the
// game itself never reports violations of: exactly one header, exactly
// one OptionSettings line, balanced quotes and parens.
func ParseINI(content string) (*INI, error) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	var header, opt string
	optCount := 0
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		switch {
		case t == "":
		case strings.HasPrefix(t, "["):
			if header != "" {
				return nil, fmt.Errorf("settings: multiple section headers")
			}
			header = t
		case strings.HasPrefix(t, "OptionSettings="):
			optCount++
			opt = t
		default:
			return nil, fmt.Errorf("settings: unexpected line %q — a stray line inside the file silently resets the server to defaults", truncate(t, 60))
		}
	}
	if header == "" {
		return nil, fmt.Errorf("settings: missing section header line")
	}
	if optCount != 1 {
		return nil, fmt.Errorf("settings: expected exactly one OptionSettings line, found %d", optCount)
	}
	body, ok := strings.CutPrefix(opt, "OptionSettings=(")
	if !ok || !strings.HasSuffix(body, ")") {
		return nil, fmt.Errorf("settings: OptionSettings is not a parenthesized tuple")
	}
	body = strings.TrimSuffix(body, ")")

	ini := &INI{Header: header, index: map[string]int{}}
	for _, kv := range splitTuple(body) {
		k, v, found := strings.Cut(kv, "=")
		if !found {
			return nil, fmt.Errorf("settings: tuple entry without '=': %q", truncate(kv, 40))
		}
		k = strings.TrimSpace(k)
		if _, dup := ini.index[k]; dup {
			return nil, fmt.Errorf("settings: duplicate key %s", k)
		}
		ini.index[k] = len(ini.pairs)
		ini.pairs = append(ini.pairs, pair{key: k, raw: v})
	}
	if pos, bad := unbalancedAt(body); bad {
		return nil, fmt.Errorf("settings: unbalanced quotes or parentheses in OptionSettings near character %d: …%s…",
			pos, truncate(contextAt(body, pos), 60))
	}
	return ini, nil
}

// splitTuple splits the tuple body on top-level commas, respecting quoted
// strings (which may contain commas, parens and '=') and nested tuples
// like CrossplayPlatforms=(Steam,Xbox,PS5,Mac). Same logic the testbox
// verify script used, ported.
func splitTuple(body string) []string {
	var out []string
	var buf strings.Builder
	depth, inQ, esc := 0, false, false
	for _, ch := range body {
		if esc { // previous char was a backslash inside quotes: literal char
			esc = false
			buf.WriteRune(ch)
			continue
		}
		switch {
		case inQ && ch == '\\':
			esc = true
		case ch == '"':
			inQ = !inQ
		case !inQ && ch == '(':
			depth++
		case !inQ && ch == ')':
			depth--
		case !inQ && ch == ',' && depth == 0:
			out = append(out, buf.String())
			buf.Reset()
			continue
		}
		buf.WriteRune(ch)
	}
	if buf.Len() > 0 {
		out = append(out, buf.String())
	}
	return out
}

func unbalancedAt(body string) (int, bool) {
	depth, inQ, esc := 0, false, false
	lastEvent := 0
	for i, ch := range body {
		if esc {
			esc = false
			continue
		}
		switch ch {
		case '\\':
			if inQ {
				esc = true
			}
		case '"':
			inQ = !inQ
			lastEvent = i
		case '(':
			if !inQ {
				depth++
				lastEvent = i
			}
		case ')':
			if !inQ {
				depth--
				lastEvent = i
				if depth < 0 {
					return i, true // a close with no open: name the exact spot
				}
			}
		}
	}
	if inQ || depth != 0 {
		return lastEvent, true
	}
	return 0, false
}

func contextAt(s string, pos int) string {
	lo, hi := pos-25, pos+25
	if lo < 0 {
		lo = 0
	}
	if hi > len(s) {
		hi = len(s)
	}
	return s[lo:hi]
}

// Get returns the raw value for key (exact case), if present.
func (i *INI) Get(key string) (string, bool) {
	idx, ok := i.index[key]
	if !ok {
		return "", false
	}
	return i.pairs[idx].raw, true
}

// Keys returns all keys in file order.
func (i *INI) Keys() []string {
	out := make([]string, len(i.pairs))
	for n, p := range i.pairs {
		out[n] = p.key
	}
	return out
}

// SetRaw replaces (or appends) a key's raw value.
func (i *INI) SetRaw(key, raw string) {
	if idx, ok := i.index[key]; ok {
		i.pairs[idx].raw = raw
		return
	}
	i.index[key] = len(i.pairs)
	i.pairs = append(i.pairs, pair{key: key, raw: raw})
}

// FormatValue renders a staged value in the exact serialization the game
// parses (key-list "file_format" rules): True/False capitalized, floats
// with six decimals, strings double-quoted, enums bare, lists raw.
func FormatValue(d *KeyDef, val any) (string, error) {
	switch d.Type {
	case "bool":
		if val.(bool) {
			return "True", nil
		}
		return "False", nil
	case "float":
		f, _ := toFloat(val)
		return fmt.Sprintf("%.6f", f), nil
	case "int":
		f, _ := toFloat(val)
		return fmt.Sprintf("%d", int64(f)), nil
	case "string":
		return `"` + val.(string) + `"`, nil
	case "enum":
		return val.(string), nil
	case "list":
		return val.(string), nil
	}
	return "", fmt.Errorf("settings: cannot format type %q", d.Type)
}

// Serialize renders the two-line file with a trailing newline.
func (i *INI) Serialize() string {
	parts := make([]string, len(i.pairs))
	for n, p := range i.pairs {
		parts[n] = p.key + "=" + p.raw
	}
	return i.Header + "\nOptionSettings=(" + strings.Join(parts, ",") + ")\n"
}

// LoadINIFile reads and parses an ini from disk.
func LoadINIFile(path string) (*INI, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("settings: read %s: %w", path, err)
	}
	return ParseINI(string(b))
}

// WriteINIFileAtomic serializes to a temp file in the same directory and
// renames it over path, then re-reads and re-parses as the post-write
// structural check (§6.3 APPLY: "verify file structure integrity after
// write").
func WriteINIFileAtomic(path string, ini *INI) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".paladin-ini-*")
	if err != nil {
		return fmt.Errorf("settings: temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename
	if _, err := tmp.WriteString(ini.Serialize()); err != nil {
		tmp.Close()
		return fmt.Errorf("settings: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("settings: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("settings: close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("settings: rename into place: %w", err)
	}
	if _, err := LoadINIFile(path); err != nil {
		return fmt.Errorf("settings: post-write structural check FAILED: %w", err)
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
