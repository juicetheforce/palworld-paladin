package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Trigger records why a backup exists (shown in the browser UI, §6.8).
type Trigger string

const (
	TriggerPreCommit  Trigger = "pre-commit"
	TriggerPreRestore Trigger = "pre-restore"
	TriggerPreUpdate  Trigger = "pre-update"
	TriggerScheduled  Trigger = "scheduled"
	TriggerManual     Trigger = "manual"
)

// Entry is one backup in the catalog.
type Entry struct {
	ID        string    `json:"id"` // directory name: <RFC3339-ish>-<trigger>
	Trigger   Trigger   `json:"trigger"`
	WorldName string    `json:"world_name"` // source folder name (the world GUID)
	Created   time.Time `json:"created"`
	TotalSize int64     `json:"total_size"`
	Path      string    `json:"-"` // absolute backup dir
}

// manifest lists every file with its size — the default integrity check
// (§6.9: manifest+sizes; checksums are a future deep-verify option, open).
type manifest struct {
	Files     []manifestFile `json:"files"`
	TotalSize int64          `json:"total_size"`
}

type manifestFile struct {
	Path string `json:"path"` // relative to the world dir
	Size int64  `json:"size"`
}

const (
	worldSubdir   = "world"
	metaFile      = "metadata.json"
	manifestFile_ = "manifest.json"
	partialPrefix = ".partial-"
)

// Manager owns a backup root directory.
type Manager struct {
	Root string
}

// NewManager ensures the root exists.
func NewManager(root string) (*Manager, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("backup root: %w", err)
	}
	return &Manager{Root: root}, nil
}

// Create copies worldDir into a new backup. Crash-safe by construction:
// work happens under a ".partial-" name and is renamed to its final name
// only when complete — a crash leaves an obviously-partial directory,
// never a plausible-looking corrupt backup. On error, partials are
// removed before returning (the engine's BACKUP-step contract).
func (m *Manager) Create(ctx context.Context, worldDir string, trigger Trigger) (*Entry, error) {
	worldName := filepath.Base(filepath.Clean(worldDir))
	id := time.Now().UTC().Format("20060102T150405Z") + "-" + string(trigger)
	final := filepath.Join(m.Root, id)
	partial := filepath.Join(m.Root, partialPrefix+id)

	cleanup := func() { os.RemoveAll(partial) }

	man, err := buildManifest(worldDir)
	if err != nil {
		return nil, fmt.Errorf("backup: scan world: %w", err)
	}
	if err := copyDir(ctx, worldDir, filepath.Join(partial, worldSubdir)); err != nil {
		cleanup()
		return nil, fmt.Errorf("backup: copy world: %w", err)
	}
	// Verify the copy against the manifest before calling it a backup.
	if err := VerifyDir(filepath.Join(partial, worldSubdir), man); err != nil {
		cleanup()
		return nil, fmt.Errorf("backup: copy failed integrity check (world changed mid-copy?): %w", err)
	}
	entry := Entry{ID: id, Trigger: trigger, WorldName: worldName,
		Created: time.Now().UTC(), TotalSize: man.TotalSize}
	if err := writeJSON(filepath.Join(partial, manifestFile_), man); err != nil {
		cleanup()
		return nil, err
	}
	if err := writeJSON(filepath.Join(partial, metaFile), entry); err != nil {
		cleanup()
		return nil, err
	}
	if err := os.Rename(partial, final); err != nil {
		cleanup()
		return nil, fmt.Errorf("backup: finalize: %w", err)
	}
	entry.Path = final
	return &entry, nil
}

// List returns finalized backups, newest first. Partial directories are
// reported via the second return so the UI can flag crash leftovers.
func (m *Manager) List() (entries []Entry, partials []string, err error) {
	des, err := os.ReadDir(m.Root)
	if err != nil {
		return nil, nil, fmt.Errorf("backup: list: %w", err)
	}
	for _, de := range des {
		if !de.IsDir() {
			continue
		}
		if strings.HasPrefix(de.Name(), partialPrefix) {
			partials = append(partials, filepath.Join(m.Root, de.Name()))
			continue
		}
		var e Entry
		if err := readJSON(filepath.Join(m.Root, de.Name(), metaFile), &e); err != nil {
			continue // not a Paladin backup; ignore
		}
		e.Path = filepath.Join(m.Root, de.Name())
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Created.After(entries[j].Created) })
	return entries, partials, nil
}

// Get finds a backup by ID.
func (m *Manager) Get(id string) (*Entry, error) {
	entries, _, err := m.List()
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].ID == id {
			return &entries[i], nil
		}
	}
	return nil, fmt.Errorf("backup: no backup with id %q", id)
}

// Verify checks a backup's world copy against its stored manifest.
func (m *Manager) Verify(e *Entry) error {
	var man manifest
	if err := readJSON(filepath.Join(e.Path, manifestFile_), &man); err != nil {
		return fmt.Errorf("backup %s: read manifest: %w", e.ID, err)
	}
	if err := VerifyDir(filepath.Join(e.Path, worldSubdir), man); err != nil {
		return fmt.Errorf("backup %s: %w", e.ID, err)
	}
	return nil
}

// WorldPath returns the directory holding the backed-up world files.
func (e *Entry) WorldPath() string { return filepath.Join(e.Path, worldSubdir) }

// Prune deletes the oldest finalized backups beyond keep. It never
// touches partials (those are crash evidence for the operator).
func (m *Manager) Prune(keep int) (deleted []string, err error) {
	entries, _, err := m.List()
	if err != nil {
		return nil, err
	}
	for i := keep; i < len(entries); i++ {
		if err := os.RemoveAll(entries[i].Path); err != nil {
			return deleted, fmt.Errorf("backup: prune %s: %w", entries[i].ID, err)
		}
		deleted = append(deleted, entries[i].ID)
	}
	return deleted, nil
}

// Delete removes a single backup by ID (manual delete from the Server
// Admin page). Refuses to touch anything that isn't a finalized backup
// directory under the root, as a guard against path surprises.
func (m *Manager) Delete(id string) error {
	if id == "" || strings.ContainsAny(id, "/\\") || strings.HasPrefix(id, ".") {
		return fmt.Errorf("backup: invalid id %q", id)
	}
	e, err := m.Get(id) // confirms it exists and is a real catalog entry
	if err != nil {
		return err
	}
	return os.RemoveAll(e.Path)
}

// Count returns the number of finalized backups in the catalog (for the
// dashboard card). Partial/interrupted dirs are not counted.
func (m *Manager) Count() (int, error) {
	entries, _, err := m.List()
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

// WorldBackupFunc adapts a Manager to the commit payload's injected
// BACKUP step (settings.CommitPayload.WorldBackup).
func WorldBackupFunc(m *Manager, worldDir string) func(context.Context) error {
	return func(ctx context.Context) error {
		_, err := m.Create(ctx, worldDir, TriggerPreCommit)
		return err
	}
}

// ---- internals -------------------------------------------------------------

func buildManifest(dir string) (manifest, error) {
	var man manifest
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		man.Files = append(man.Files, manifestFile{Path: rel, Size: info.Size()})
		man.TotalSize += info.Size()
		return nil
	})
	sort.Slice(man.Files, func(i, j int) bool { return man.Files[i].Path < man.Files[j].Path })
	return man, err
}

// VerifyDir checks dir contains exactly the manifest's files with exact
// sizes — extra files fail too (a backup must match, both directions).
func VerifyDir(dir string, man manifest) error {
	got, err := buildManifest(dir)
	if err != nil {
		return fmt.Errorf("integrity scan: %w", err)
	}
	if len(got.Files) != len(man.Files) {
		return fmt.Errorf("integrity: file count %d != manifest %d", len(got.Files), len(man.Files))
	}
	for i := range man.Files {
		if got.Files[i] != man.Files[i] {
			return fmt.Errorf("integrity: %s (size %d) != manifest %s (size %d)",
				got.Files[i].Path, got.Files[i].Size, man.Files[i].Path, man.Files[i].Size)
		}
	}
	return nil
}

func copyDir(ctx context.Context, src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o750)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o640)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
