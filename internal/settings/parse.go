package settings

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseValue converts operator string input (CLI flag, web form) into the
// typed value ValidateStaged expects, per the key's definition. It parses
// only — validation (ranges, enum membership, quote safety) remains
// ValidateStaged's job, so error messages stay in one place.
func ParseValue(d *KeyDef, s string) (any, error) {
	switch d.Type {
	case "bool":
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "true", "1", "yes", "on":
			return true, nil
		case "false", "0", "no", "off":
			return false, nil
		}
		return nil, fmt.Errorf("%s: %q is not a boolean (use true/false)", d.Key, s)
	case "float", "int":
		f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a number", d.Key, s)
		}
		return f, nil
	case "string", "enum", "list":
		return s, nil
	}
	return nil, fmt.Errorf("%s: key list declares unknown type %q", d.Key, d.Type)
}
