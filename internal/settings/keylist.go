package settings

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/juicetheforce/palworld-paladin/data"
)

// KeyDef is one entry from the verified key list
// (data/palworld-settings.json). Field semantics per DESIGN.md §6.2.
type KeyDef struct {
	Key          string   `json:"key"`
	Category     string   `json:"category"`
	Type         string   `json:"type"` // bool|float|int|string|enum|list
	Default      any      `json:"default"`
	Min          *float64 `json:"min,omitempty"`
	Max          *float64 `json:"max,omitempty"`
	Enum         []string `json:"enum,omitempty"`
	AddedIn      *string  `json:"added_in"`
	Source       string   `json:"source"` // official|community|ea-carryover|deprecated
	Tooltip      string   `json:"tooltip"`
	Gotcha       *string  `json:"gotcha"`
	KBLink       *string  `json:"kb_link"`
	Verify       []string `json:"verify"`
	RestReadback *bool    `json:"rest_readback,omitempty"` // false = never echoed by GET /settings
	Protected    *string  `json:"protected,omitempty"`     // set = Paladin refuses to edit this key; value explains why
}

// ReadbackVerifiable reports whether GET /settings can confirm this key
// (passwords cannot; verified 2026-07-27).
func (k KeyDef) ReadbackVerifiable() bool {
	return k.RestReadback == nil || *k.RestReadback
}

// KeyList is the loaded artifact with case-insensitive lookup — required
// because the REST readback returns at least one key with different
// casing than the ini (AutoSaveSpan → autoSaveSpan; DESIGN.md §6.3).
type KeyList struct {
	GameVersion string
	Keys        []KeyDef
	byLower     map[string]*KeyDef
}

type keyListFile struct {
	GameVersion string   `json:"game_version"`
	Keys        []KeyDef `json:"keys"`
}

// LoadKeyList parses the embedded artifact.
func LoadKeyList() (*KeyList, error) {
	return ParseKeyList(data.SettingsJSON)
}

// ParseKeyList parses key-list JSON (exposed for tests and tooling).
func ParseKeyList(b []byte) (*KeyList, error) {
	var f keyListFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("settings: parse key list: %w", err)
	}
	if len(f.Keys) == 0 {
		return nil, fmt.Errorf("settings: key list contains no keys")
	}
	kl := &KeyList{GameVersion: f.GameVersion, Keys: f.Keys,
		byLower: make(map[string]*KeyDef, len(f.Keys))}
	for i := range kl.Keys {
		kl.byLower[strings.ToLower(kl.Keys[i].Key)] = &kl.Keys[i]
	}
	return kl, nil
}

// Lookup finds a key definition case-insensitively.
func (kl *KeyList) Lookup(key string) (*KeyDef, bool) {
	d, ok := kl.byLower[strings.ToLower(key)]
	return d, ok
}

// ValidateStaged checks a staged diff (ini key → desired value) against
// the key list: known keys, correct exact casing, right types, ranges,
// enum membership, and no deprecated keys. This is the PRE-CHECK's
// payload half (§6.3 step 1).
func (kl *KeyList) ValidateStaged(diff map[string]any) error {
	for key := range diff {
		if d, ok := kl.Lookup(key); ok && d.Protected != nil {
			return fmt.Errorf("%s is protected: %s", key, *d.Protected)
		}
	}
	return kl.validateStagedValues(diff)
}

func (kl *KeyList) validateStagedValues(diff map[string]any) error {
	var errs []string
	for key, val := range diff {
		d, ok := kl.Lookup(key)
		if !ok {
			errs = append(errs, fmt.Sprintf("%s: unknown key (not in the v1.0 key list)", key))
			continue
		}
		// Exact casing matters when WRITING the ini (the game ignores
		// unknown spellings silently — the Decreace lesson); the staged
		// diff must use the canonical name.
		if d.Key != key {
			errs = append(errs, fmt.Sprintf("%s: wrong casing; the ini key is exactly %q", key, d.Key))
			continue
		}
		if d.Source == "deprecated" {
			errs = append(errs, fmt.Sprintf("%s: deprecated/superseded key; not writable", key))
			continue
		}
		if msg := checkValue(d, val); msg != "" {
			errs = append(errs, key+": "+msg)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("staged diff invalid:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

func checkValue(d *KeyDef, val any) string {
	switch d.Type {
	case "bool":
		if _, ok := val.(bool); !ok {
			return fmt.Sprintf("want bool, got %T", val)
		}
	case "float", "int":
		f, ok := toFloat(val)
		if !ok {
			return fmt.Sprintf("want number, got %T", val)
		}
		if d.Type == "int" && f != float64(int64(f)) {
			return fmt.Sprintf("want integer, got %v", val)
		}
		if d.Min != nil && f < *d.Min {
			return fmt.Sprintf("%v below minimum %v", f, *d.Min)
		}
		if d.Max != nil && f > *d.Max {
			return fmt.Sprintf("%v above maximum %v", f, *d.Max)
		}
	case "enum":
		s, ok := val.(string)
		if !ok {
			return fmt.Sprintf("want one of %v, got %T", d.Enum, val)
		}
		for _, e := range d.Enum {
			if e == s {
				return ""
			}
		}
		return fmt.Sprintf("%q is not one of %v", s, d.Enum)
	case "string":
		s, ok := val.(string)
		if !ok {
			return fmt.Sprintf("want string, got %T", val)
		}
		// The tuple format has no escaping; an embedded quote silently
		// resets the whole file to defaults. Refuse it here.
		if strings.ContainsAny(s, `"`+"\n") {
			return "strings may not contain quotes or newlines (would corrupt the OptionSettings line)"
		}
	case "list":
		s, ok := val.(string)
		if !ok || !strings.HasPrefix(s, "(") || !strings.HasSuffix(s, ")") {
			return `list values are raw tuples like "(Steam,Xbox,PS5,Mac)"`
		}
	default:
		return fmt.Sprintf("key list declares unknown type %q", d.Type)
	}
	return ""
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}
