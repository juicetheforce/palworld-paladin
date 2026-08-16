package settings

import (
	"fmt"
)

// preservedOnReset are the keys a full server reset must NOT return to
// game defaults: they are how Paladin and your players reach the server
// at all. Resetting RESTAPIEnabled or AdminPassword would leave the tool
// locked out of the very server it just rebuilt.
var preservedOnReset = map[string]bool{
	"AdminPassword":  true,
	"ServerPassword": true,
	"RESTAPIEnabled": true,
	"RESTAPIPort":    true,
	"RCONEnabled":    true,
	"RCONPort":       true,
	"PublicPort":     true,
	"PublicIP":       true,
	"ServerName":     true,
	"ServerDescription": true,
}

// ResetFileToDefaults rewrites every non-preserved key in the ini to its
// documented game default, leaving connectivity and identity keys alone.
// Used by the full-server-reset cycle; the caller has already taken a
// pre-reset backup and holds the pre-write ini copy.
func ResetFileToDefaults(path string, kl *KeyList) error {
	if kl == nil {
		return fmt.Errorf("settings: no key list")
	}
	ini, err := LoadINIFile(path)
	if err != nil {
		return fmt.Errorf("settings: load ini for reset: %w", err)
	}
	for i := range kl.Keys {
		def := &kl.Keys[i]
		// Protected keys are off-limits by the key list's own decree;
		// preservedOnReset adds the connectivity/identity set that a
		// reset must not clobber.
		if def.Protected != nil || preservedOnReset[def.Key] {
			continue
		}
		if def.Default == nil {
			continue
		}
		raw, err := FormatValue(def, def.Default)
		if err != nil {
			// A key whose default cannot be formatted is a data-file bug,
			// not a reason to abandon the reset: leave that key as-is.
			continue
		}
		ini.SetRaw(def.Key, raw)
	}
	return WriteINIFileAtomic(path, ini)
}
