package settings

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/juicetheforce/palworld-paladin/internal/maintain"
)

// CommitPayload is the settings-commit half of the shared maintenance
// state machine (DESIGN.md §6.3): the engine owns the invariants, this
// payload owns the ini mutation and the honest VERIFY.
type CommitPayload struct {
	// KeyList: the loaded, verified artifact.
	KeyList *KeyList
	// INIPath: the live PalWorldSettings.ini.
	INIPath string
	// WorldDir: the active world folder (for WorldOption.sav detection).
	WorldDir string

	clearWorldOption bool   // set by PreCheck: staged world keys + override present
	clearedWorldOpt  string // set by Apply: where the override was moved
	// Staged: ini key → desired value (canonical casing).
	Staged map[string]any
	// WorldBackup performs the BACKUP step (world-folder copy); supplied
	// by the backup package. Must clean its own partials on error.
	WorldBackup func(ctx context.Context) error
	// ReadSettings performs the VERIFY readback (palapi Settings).
	ReadSettings func(ctx context.Context) (map[string]any, error)
	// BackupAnchor: path reported as the world-backup recovery anchor.
	BackupAnchor string

	prevPath string   // pre-write ini copy (the APPLY rollback anchor)
	warnings []string // collected in PreCheck, surfaced in Verify
}

// compile-time check: CommitPayload is a maintain.Payload.
var _ maintain.Payload = (*CommitPayload)(nil)

func (p *CommitPayload) Name() string { return "commit" }

// PreCheck validates the staged diff, the current file's structure, and
// detects WorldOption.sav (§11: community consensus is that it overrides
// the ini for keys it contains on an existing world; exact coverage is
// still an open item, so the warning is general and honest).
func (p *CommitPayload) PreCheck(ctx context.Context) error {
	if len(p.Staged) == 0 {
		return fmt.Errorf("nothing staged")
	}
	if p.KeyList == nil || p.WorldBackup == nil || p.ReadSettings == nil {
		return fmt.Errorf("commit payload not fully wired")
	}
	if err := p.KeyList.ValidateStaged(p.Staged); err != nil {
		return err
	}
	if _, err := LoadINIFile(p.INIPath); err != nil {
		return fmt.Errorf("current ini failed structural validation (fix before committing): %w", err)
	}
	p.warnings = nil
	p.clearWorldOption = false
	if p.WorldDir != "" {
		if _, err := os.Stat(filepath.Join(p.WorldDir, "WorldOption.sav")); err == nil {
			// §11 CLOSED (verified on a fresh world AND the live box,
			// 2026-08-07): on an existing world the server reads gameplay
			// keys from WorldOption.sav and IGNORES the ini for them;
			// identity keys (name, passwords, ports, caps) still come from
			// the ini. A commit that only touches identity keys can leave
			// the file alone; anything else must clear it during APPLY
			// (while the server is stopped and the cycle backup already
			// holds a copy) or the commit is a silent no-op in-game.
			if stagedTouchesWorldKeys(p.Staged) {
				p.clearWorldOption = true
			}
		}
	}
	return nil
}

// identityKeys are read from the ini even on existing worlds (verified:
// changing these through a commit works with WorldOption.sav present).
// Everything else is assumed world-owned once a WorldOption.sav exists —
// over-clearing is harmless (the just-written ini is the source of truth),
// under-clearing silently defeats the commit.
var identityKeys = map[string]bool{
	"ServerName": true, "ServerDescription": true,
	"AdminPassword": true, "ServerPassword": true,
	"PublicIP": true, "PublicPort": true,
	"ServerPlayerMaxNum": true,
	"RCONEnabled":        true, "RCONPort": true,
	"RESTAPIEnabled": true, "RESTAPIPort": true,
	"Region": true, "bShowPlayerList": true,
	"LogFormatType": true, "CrossplayPlatforms": true,
	"bIsUseBackupSaveData": true,
}

func stagedTouchesWorldKeys(staged map[string]any) bool {
	for k := range staged {
		if !identityKeys[k] {
			return true
		}
	}
	return false
}

// Backup delegates the world copy to the backup package's function.
func (p *CommitPayload) Backup(ctx context.Context) error { return p.WorldBackup(ctx) }

// Apply takes the pre-write copy, stages the values into the parsed ini
// with exact game serialization, and writes atomically with post-write
// structural validation (invariant I5 + §6.3 APPLY).
func (p *CommitPayload) Apply(ctx context.Context) error {
	orig, err := os.ReadFile(p.INIPath)
	if err != nil {
		return fmt.Errorf("read ini: %w", err)
	}
	p.prevPath = p.INIPath + ".paladin-prev"
	if err := os.WriteFile(p.prevPath, orig, 0o640); err != nil {
		return fmt.Errorf("pre-write copy (rollback anchor): %w", err)
	}
	ini, err := ParseINI(string(orig))
	if err != nil {
		return fmt.Errorf("parse ini: %w", err)
	}
	for key, val := range p.Staged {
		def, _ := p.KeyList.Lookup(key) // validated in PreCheck
		raw, err := FormatValue(def, val)
		if err != nil {
			return err
		}
		ini.SetRaw(def.Key, raw)
	}
	if err := WriteINIFileAtomic(p.INIPath, ini); err != nil {
		return err
	}
	// Neutralize the WorldOption.sav override so the ini just written is
	// what the server actually loads (the core-promise fix). Rename, not
	// delete: instantly restorable by hand, and RollbackApply undoes it.
	// The cycle backup taken at BACKUP also holds the original.
	if p.clearWorldOption {
		src := filepath.Join(p.WorldDir, "WorldOption.sav")
		dst := fmt.Sprintf("%s.pre-paladin-%d", src, time.Now().Unix())
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("clear WorldOption.sav override: %w", err)
		}
		p.clearedWorldOpt = dst
	}
	return nil
}

// RollbackApply restores the pre-write copy byte-for-byte.
func (p *CommitPayload) RollbackApply(ctx context.Context) error {
	if p.prevPath == "" {
		return fmt.Errorf("no pre-write copy exists (apply never ran?)")
	}
	b, err := os.ReadFile(p.prevPath)
	if err != nil {
		return fmt.Errorf("read pre-write copy: %w", err)
	}
	if err := os.WriteFile(p.INIPath, b, 0o640); err != nil {
		return fmt.Errorf("restore ini: %w", err)
	}
	if _, err := LoadINIFile(p.INIPath); err != nil {
		return fmt.Errorf("restored ini failed validation: %w", err)
	}
	if p.clearedWorldOpt != "" {
		if err := os.Rename(p.clearedWorldOpt, filepath.Join(p.WorldDir, "WorldOption.sav")); err != nil {
			return fmt.Errorf("restore WorldOption.sav: %w", err)
		}
		p.clearedWorldOpt = ""
	}
	return nil
}

// Verify reads back effective settings and reports honestly (§6.3 step 8
// readback rules, verified 2026-07-27):
//   - readback matching is CASE-INSENSITIVE (AutoSaveSpan → autoSaveSpan)
//   - rest_readback:false keys report "applied — not verifiable"
//   - gotcha context is attached to changed keys
//   - WorldOption.sav warnings from PreCheck surface here
func (p *CommitPayload) Verify(ctx context.Context) (maintain.VerifyResult, error) {
	var res maintain.VerifyResult
	// PreCheck's WorldOption.sav caution is actionable (a staged key may
	// silently not take on this world) → warning.
	res.Warnings = append(res.Warnings, p.warnings...)
	if p.clearedWorldOpt != "" {
		res.Notes = append(res.Notes, fmt.Sprintf(
			"WorldOption.sav override cleared (saved as %s) — the ini is now authoritative for this world", filepath.Base(p.clearedWorldOpt)))
	}

	live, err := p.ReadSettings(ctx)
	if err != nil {
		return res, fmt.Errorf("settings readback failed: %w", err)
	}
	lower := make(map[string]any, len(live))
	for k, v := range live {
		lower[strings.ToLower(k)] = v
	}

	for key, want := range p.Staged {
		def, _ := p.KeyList.Lookup(key)
		if !def.ReadbackVerifiable() {
			// Informational: applied, just not confirmable by readback.
			res.Notes = append(res.Notes, fmt.Sprintf(
				"%s: applied — not verifiable via readback (the API never echoes this key); the file is authoritative", def.Key))
		} else {
			got, present := lower[strings.ToLower(def.Key)]
			switch {
			case !present:
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: not present in readback — cannot confirm", def.Key))
			case !valuesMatch(def, want, got):
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: readback shows %v, expected %v — value did not take", def.Key, got, want))
			}
		}
		// Gotcha context is informational — it explains expected in-game
		// behavior (e.g. level-gated cap), it is not a failure.
		if def.Gotcha != nil {
			res.Notes = append(res.Notes, fmt.Sprintf("%s (note): %s", def.Key, *def.Gotcha))
		}
	}
	return res, nil
}

// valuesMatch compares a staged value with a readback value, tolerating
// the API's representation choices (numbers as floats, bools sometimes
// as strings, enums as strings).
func valuesMatch(def *KeyDef, want, got any) bool {
	switch def.Type {
	case "bool":
		wb := want.(bool)
		switch g := got.(type) {
		case bool:
			return g == wb
		case string:
			return strings.EqualFold(g, strconv.FormatBool(wb)) ||
				(wb && strings.EqualFold(g, "True")) || (!wb && strings.EqualFold(g, "False"))
		}
	case "float", "int":
		wf, _ := toFloat(want)
		switch g := got.(type) {
		case float64:
			return abs(g-wf) < 1e-6
		case string:
			gf, err := strconv.ParseFloat(g, 64)
			return err == nil && abs(gf-wf) < 1e-6
		}
	case "string", "enum", "list":
		ws := fmt.Sprint(want)
		gs := fmt.Sprint(got)
		return strings.Trim(gs, `"`) == strings.Trim(ws, `"`)
	}
	return false
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// Anchors names the recovery anchors (invariant I7).
func (p *CommitPayload) Anchors() []string {
	a := []string{}
	if p.prevPath != "" {
		a = append(a, p.prevPath)
	} else {
		a = append(a, p.INIPath+".paladin-prev (pre-write ini copy)")
	}
	if p.BackupAnchor != "" {
		a = append(a, p.BackupAnchor)
	}
	return a
}
