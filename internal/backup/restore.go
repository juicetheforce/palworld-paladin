package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/juicetheforce/palworld-paladin/internal/maintain"
)

// RestorePayload is the world-restore half of the shared maintenance
// state machine (DESIGN.md §6.8): same engine as commit, different
// mutation.
//
// Mechanics (rev 3): the active world folder is RENAMED aside — that
// rename IS the pre-restore safety backup, atomic because it stays a
// sibling on the same filesystem (the backup root may be a different
// mount, so the safety copy deliberately does not live there). Then the
// selected backup is copied into place and integrity-verified.
type RestorePayload struct {
	Mgr *Manager
	// Selected: the catalog entry to restore.
	Selected *Entry
	// WorldDir: the ACTIVE world folder (SaveGames/0/<guid>).
	WorldDir string
	// ReadWorldGUID returns the live server's world GUID after restart
	// (palapi Info().WorldGUID) for the identity VERIFY.
	ReadWorldGUID func(ctx context.Context) (string, error)
	// SafetyRelocateDir, if set, is where the pre-restore safety copy is
	// moved AFTER the restore verifies healthy — Paladin's own root,
	// outside the game tree (the "removable guest" principle, §2). The
	// rename-aside itself always lands in a dot-scratch sibling first
	// (atomicity needs same-filesystem); relocation happens last. If
	// empty, the safety copy is left in the dot-scratch sibling.
	SafetyRelocateDir string

	safetyPath string // current location of the safety copy
}

var _ maintain.Payload = (*RestorePayload)(nil)

func (p *RestorePayload) Name() string { return "restore" }

// PreCheck verifies the selected backup against its manifest while the
// server is still up — a restore must never discover a corrupt archive
// after stopping the world (§6.8 step 2).
func (p *RestorePayload) PreCheck(ctx context.Context) error {
	if p.Mgr == nil || p.Selected == nil || p.ReadWorldGUID == nil {
		return fmt.Errorf("restore payload not fully wired")
	}
	if fi, err := os.Stat(p.WorldDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("active world dir %s: %v", p.WorldDir, err)
	}
	if err := p.Mgr.Verify(p.Selected); err != nil {
		return fmt.Errorf("selected backup failed integrity check: %w", err)
	}
	return nil
}

// Backup is the SAFETY-BACKUP step: rename the active world aside.
func (p *RestorePayload) Backup(ctx context.Context) error {
	// Dot-prefixed so the world detector skips it (§6.9). Same parent dir
	// as the world, so the rename is atomic on one filesystem.
	parent := filepath.Dir(p.WorldDir)
	base := filepath.Base(p.WorldDir)
	p.safetyPath = filepath.Join(parent, ".paladin-safety-"+base+"-"+time.Now().UTC().Format("20060102T150405Z"))
	if err := os.Rename(p.WorldDir, p.safetyPath); err != nil {
		p.safetyPath = ""
		return fmt.Errorf("rename-aside safety backup: %w", err)
	}
	return nil
}

// Apply copies the selected backup into the active world path and
// verifies the copy against the backup's manifest.
func (p *RestorePayload) Apply(ctx context.Context) error {
	if err := copyDir(ctx, p.Selected.WorldPath(), p.WorldDir); err != nil {
		os.RemoveAll(p.WorldDir) // partial copy is worse than absence
		return fmt.Errorf("copy backup into place: %w", err)
	}
	if err := p.Mgr.Verify(p.Selected); err != nil { // source still intact?
		return fmt.Errorf("backup changed during restore?!: %w", err)
	}
	var man manifest
	if err := readJSON(filepath.Join(p.Selected.Path, manifestFile_), &man); err != nil {
		return err
	}
	if err := VerifyDir(p.WorldDir, man); err != nil {
		os.RemoveAll(p.WorldDir)
		return fmt.Errorf("restored world failed integrity check: %w", err)
	}
	return nil
}

// RollbackApply removes any partial restored copy and renames the safety
// backup (the original world) back into place.
func (p *RestorePayload) RollbackApply(ctx context.Context) error {
	if p.safetyPath == "" {
		return fmt.Errorf("no safety backup exists (rename-aside never ran?)")
	}
	if _, err := os.Stat(p.WorldDir); err == nil {
		if err := os.RemoveAll(p.WorldDir); err != nil {
			return fmt.Errorf("remove partial restore: %w", err)
		}
	}
	if err := os.Rename(p.safetyPath, p.WorldDir); err != nil {
		return fmt.Errorf("rename safety backup back into place: %w", err)
	}
	p.safetyPath = ""
	return nil
}

// Verify confirms the live server is serving the restored world: the
// world GUID reported by /info must equal the restored folder's name
// (they are the same identifier — verified on the test box).
func (p *RestorePayload) Verify(ctx context.Context) (maintain.VerifyResult, error) {
	var res maintain.VerifyResult
	guid, err := p.ReadWorldGUID(ctx)
	if err != nil {
		return res, fmt.Errorf("world identity readback failed: %w", err)
	}
	want := filepath.Base(filepath.Clean(p.WorldDir))
	if guid != want {
		// Actionable: the server may not be serving what we restored.
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"world identity mismatch: server reports GUID %s, restored folder is %s — the server may not be serving the restored world", guid, want))
	}
	// Restore verified healthy → relocate the safety copy out of the game
	// save tree into Paladin's own root ("removable guest", §2). This is
	// housekeeping AFTER success, so a relocation error is a warning, not
	// a failure — the restore already stands.
	if p.SafetyRelocateDir != "" && p.safetyPath != "" {
		if moved, err := relocate(p.safetyPath, p.SafetyRelocateDir); err != nil {
			// A relocation failure IS actionable (a Paladin artifact is
			// left in the save tree) — warning, not note.
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"restored OK, but could not relocate the safety copy out of the save tree (%v); it remains at %s", err, p.safetyPath))
		} else {
			p.safetyPath = moved
		}
	}
	// Purely informational: a receipt of what was restored and where the
	// reversible safety copy lives.
	res.Notes = append(res.Notes, fmt.Sprintf(
		"restored from backup %s (%s, created %s); the pre-restore safety copy of the previous world is at %s",
		p.Selected.ID, p.Selected.Trigger, p.Selected.Created.Format(time.RFC3339), p.safetyPath))
	return res, nil
}

// relocate moves src into dstDir, returning the new path. Atomic rename
// when same-filesystem, else copy-then-delete (safe: only called after a
// verified-healthy restore).
func relocate(src, dstDir string) (string, error) {
	if err := os.MkdirAll(dstDir, 0o750); err != nil {
		return src, err
	}
	dst := filepath.Join(dstDir, filepath.Base(src))
	if err := os.Rename(src, dst); err == nil {
		return dst, nil
	}
	if err := copyDir(context.Background(), src, dst); err != nil {
		os.RemoveAll(dst)
		return src, err
	}
	if err := os.RemoveAll(src); err != nil {
		return dst, fmt.Errorf("copied but could not remove original: %w", err)
	}
	return dst, nil
}

// Anchors names the recovery anchors (invariant I7).
func (p *RestorePayload) Anchors() []string {
	a := []string{p.Selected.Path + " (selected backup)"}
	if p.safetyPath != "" {
		a = append(a, p.safetyPath+" (pre-restore safety copy of the previous world)")
	}
	return a
}

// ---- disk pre-flight (invariant I4) ----------------------------------------

// FreeBytes reports the free space on the filesystem containing path.
func FreeBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// DiskCheckFunc returns an engine DiskCheck (maintain.Config.DiskCheck)
// enforcing free space ≥ factor × current world size on the world's
// filesystem. The design default is factor 2 plus headroom (§6.9 I4).
func DiskCheckFunc(worldDir string, factor float64) func() error {
	return func() error {
		man, err := buildManifest(worldDir)
		if err != nil {
			return fmt.Errorf("disk pre-flight: measure world: %w", err)
		}
		free, err := FreeBytes(worldDir)
		if err != nil {
			return fmt.Errorf("disk pre-flight: %w", err)
		}
		need := uint64(float64(man.TotalSize) * factor)
		if free < need {
			return fmt.Errorf("disk pre-flight: need %d bytes free (%.1f× world size %d), have %d",
				need, factor, man.TotalSize, free)
		}
		return nil
	}
}
