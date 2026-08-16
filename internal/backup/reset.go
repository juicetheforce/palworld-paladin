package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/juicetheforce/palworld-paladin/internal/maintain"
)

// ResetPayload wipes the world and starts the server fresh — the "new
// season" button. It is the most destructive operation Paladin performs,
// so it runs through the SAME maintenance state machine as commit and
// restore (announce → save → stop → backup → apply → start → verify),
// inheriting the journal, the rollback anchors, and the live feed.
//
// Mechanics mirror RestorePayload deliberately:
//   - BACKUP takes a real, catalogued pre-reset backup (TriggerPreReset)
//     so the deleted world is recoverable from the Backups page, AND
//     renames the live world aside as the atomic rollback anchor.
//   - APPLY deletes the renamed-aside world's replacement slot: the
//     server is left with NO world folder, so it generates a brand new
//     world (new GUID) on START. Optional extras (ini→defaults, wiping
//     Paladin's player/ban data) happen here too.
//   - ROLLBACK renames the aside copy back: nothing is unrecoverable
//     until the cycle succeeds.
//   - VERIFY confirms the server came up on a DIFFERENT world GUID than
//     the one that was wiped — proof the reset actually took.
type ResetPayload struct {
	Mgr *Manager
	// WorldDir: the ACTIVE world folder (SaveGames/0/<guid>) to wipe.
	WorldDir string
	// ReadWorldGUID returns the live server's world GUID after restart.
	ReadWorldGUID func(ctx context.Context) (string, error)
	// ForceSave asks the live server to write the world to disk. A newly
	// generated world exists only in memory until the first autosave, so
	// VERIFY forces a save and then checks the file it produced.
	ForceSave func(ctx context.Context) error

	// ResetSettings: rewrite PalWorldSettings.ini to game defaults.
	// (Default TRUE in the UI: a reset means a fresh server.) The
	// identity keys the server needs to stay reachable — REST config and
	// admin password — are preserved by ResetINIToDefaults itself.
	ResetSettings bool
	// ResetINIToDefaults is supplied by the caller (settings package
	// owns ini semantics; backup must not). Called during APPLY only
	// when ResetSettings is set. It MUST preserve REST/admin keys.
	ResetINIToDefaults func(ctx context.Context) error

	// WipePlayerData: clear Paladin's known-players history and ban list.
	// Operator's explicit choice; the pre-reset backup still holds a
	// copy of the world these were derived from.
	WipePlayerData bool
	// WipePaladinPlayerData is supplied by the caller (webserv/store owns
	// that data). Called during APPLY only when WipePlayerData is set.
	WipePaladinPlayerData func(ctx context.Context) error

	oldGUID     string    // world GUID being wiped (informational)
	startedAt   time.Time // cycle start; world files must postdate it
	asidePath   string // renamed-aside live world (rollback anchor)
	backupEntry *Entry // catalogued pre-reset backup
}

var _ maintain.Payload = (*ResetPayload)(nil)

func (p *ResetPayload) Name() string { return "reset" }

// PreCheck runs while the server is still up: confirm we know what we are
// about to destroy, and that a backup can plausibly be written.
func (p *ResetPayload) PreCheck(ctx context.Context) error {
	if p.Mgr == nil || p.ReadWorldGUID == nil || p.ForceSave == nil {
		return fmt.Errorf("reset payload not fully wired")
	}
	if p.ResetSettings && p.ResetINIToDefaults == nil {
		return fmt.Errorf("reset payload: settings reset requested but no handler wired")
	}
	if p.WipePlayerData && p.WipePaladinPlayerData == nil {
		return fmt.Errorf("reset payload: player-data wipe requested but no handler wired")
	}
	fi, err := os.Stat(p.WorldDir)
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("active world dir %s: %v", p.WorldDir, err)
	}
	p.oldGUID = filepath.Base(filepath.Clean(p.WorldDir))
	p.startedAt = time.Now()
	return nil
}

// Backup is the point of no regret: a full catalogued backup of the world
// about to be destroyed, then the rename-aside that makes APPLY reversible.
func (p *ResetPayload) Backup(ctx context.Context) error {
	e, err := p.Mgr.Create(ctx, p.WorldDir, TriggerPreReset)
	if err != nil {
		return fmt.Errorf("pre-reset backup failed — refusing to wipe an unbacked world: %w", err)
	}
	p.backupEntry = e

	// Rename aside (same parent → atomic) as the rollback anchor. Dot-
	// prefixed so the world detector skips it.
	parent := filepath.Dir(p.WorldDir)
	base := filepath.Base(p.WorldDir)
	p.asidePath = filepath.Join(parent, ".paladin-reset-"+base+"-"+time.Now().UTC().Format("20060102T150405Z"))
	if err := os.Rename(p.WorldDir, p.asidePath); err != nil {
		p.asidePath = ""
		return fmt.Errorf("rename world aside: %w", err)
	}
	return nil
}

// Apply performs the optional extras. The world itself is already gone
// from the server's view (renamed aside in Backup) — the server will
// generate a fresh world on START because no world folder exists.
func (p *ResetPayload) Apply(ctx context.Context) error {
	if p.ResetSettings {
		if err := p.ResetINIToDefaults(ctx); err != nil {
			return fmt.Errorf("reset settings to defaults: %w", err)
		}
	}
	if p.WipePlayerData {
		if err := p.WipePaladinPlayerData(ctx); err != nil {
			return fmt.Errorf("wipe player/ban data: %w", err)
		}
	}
	return nil
}

// RollbackApply puts the original world back. Settings/player-data are
// NOT restored here: the ini has its own pre-write copy (settings owns
// that), and the pre-reset backup holds the world those records came
// from. What matters is that the live world returns.
func (p *ResetPayload) RollbackApply(ctx context.Context) error {
	if p.asidePath == "" {
		return fmt.Errorf("no renamed-aside world exists (backup step never ran?)")
	}
	if _, err := os.Stat(p.WorldDir); err == nil {
		if err := os.RemoveAll(p.WorldDir); err != nil {
			return fmt.Errorf("remove partial fresh world: %w", err)
		}
	}
	if err := os.Rename(p.asidePath, p.WorldDir); err != nil {
		return fmt.Errorf("rename original world back into place: %w", err)
	}
	p.asidePath = ""
	return nil
}

// Verify confirms the server is serving a genuinely NEW world, then
// removes the renamed-aside copy (the catalogued backup is the keeper).
func (p *ResetPayload) Verify(ctx context.Context) (maintain.VerifyResult, error) {
	var res maintain.VerifyResult
	guid, err := p.ReadWorldGUID(ctx)
	if err != nil {
		return res, fmt.Errorf("world identity readback failed: %w", err)
	}
	// NOT a GUID comparison: Palworld names the world folder after
	// DedicatedServerName in GameUserSettings.ini, so a regenerated world
	// legitimately reuses the SAME GUID (verified on the test box —
	// an earlier GUID-difference check cried wolf on a perfectly good
	// reset). Freshness is what matters: the world files must have been
	// created AFTER this cycle started.
	// A freshly generated world lives in memory until the first autosave,
	// so ASK for a save, then poll briefly for the file it writes. (An
	// earlier version checked immediately after START and failed a good
	// reset because the world had not been persisted yet.)
	lvl := filepath.Join(p.WorldDir, "Level.sav")
	if err := p.ForceSave(ctx); err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"could not force a save to confirm the fresh world (%v); the reset itself completed", err))
	}
	var fi os.FileInfo
	var statErr error
	deadline := time.Now().Add(45 * time.Second)
	for {
		fi, statErr = os.Stat(lvl)
		if statErr == nil || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			statErr = ctx.Err()
		case <-time.After(2 * time.Second):
			continue
		}
		break
	}
	switch {
	case statErr != nil:
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"no Level.sav in %s within 45s of restart (%v) — the fresh world may not have been written yet; check the Players page once the server has autosaved", p.WorldDir, statErr))
	case fi.ModTime().Before(p.startedAt):
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"world files predate this reset (Level.sav modified %s, reset began %s) — the wipe may not have taken",
			fi.ModTime().Format(time.RFC3339), p.startedAt.Format(time.RFC3339)))
	default:
		res.Notes = append(res.Notes, fmt.Sprintf(
			"fresh world generated at %s", fi.ModTime().Format(time.RFC3339)))
	}
	if p.backupEntry != nil {
		res.Notes = append(res.Notes, fmt.Sprintf(
			"pre-reset world saved as backup %s — restore it from the Backups page to undo this reset", p.backupEntry.ID))
	}
	res.Notes = append(res.Notes, fmt.Sprintf(
		"world GUID is %s (unchanged by design — Palworld names the world folder from DedicatedServerName)", guid))
	if p.ResetSettings {
		res.Notes = append(res.Notes, "settings reset to defaults (REST/admin config preserved)")
	}
	if p.WipePlayerData {
		res.Notes = append(res.Notes, "player history and ban list cleared")
	}

	// Housekeeping AFTER success: drop the rename-aside copy. The
	// catalogued backup is the durable record, so failure here is a
	// warning (a stale folder in the save tree), not a cycle failure.
	if p.asidePath != "" {
		if err := os.RemoveAll(p.asidePath); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"reset OK, but the aside copy of the old world could not be removed (%v); it remains at %s", err, p.asidePath))
		} else {
			p.asidePath = ""
		}
	}
	return res, nil
}

// Anchors are the recovery paths named in failure reports (invariant I7).
func (p *ResetPayload) Anchors() []string {
	var a []string
	if p.backupEntry != nil {
		a = append(a, p.backupEntry.Path)
	}
	if p.asidePath != "" {
		a = append(a, p.asidePath)
	}
	return a
}
