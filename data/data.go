// Package data embeds Paladin's data artifacts into the binary
// (DESIGN.md §5.4: single static binary; the key list stays an editable,
// community-PR-able file in the repo and ships inside the executable).
package data

import _ "embed"

// SettingsJSON is data/palworld-settings.json — the verified key list:
// source of truth for the settings form, validation, tooltips, gotcha
// surfacing, and readback rules (DESIGN.md §6.2).
//
//go:embed palworld-settings.json
var SettingsJSON []byte
