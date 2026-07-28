package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juicetheforce/palworld-paladin/internal/maintain"
)

// makeWorld creates a fake world folder with a few files.
func makeWorld(t *testing.T, dir string, marker string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "Players"), 0o750); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"Level.sav":        "LEVELDATA-" + marker,
		"LevelMeta.sav":    "META-" + marker,
		"Players/0001.sav": "PLAYER-" + marker,
		"WorldOption.sav":  "OPTS-" + marker,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
}

func readMarker(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "Level.sav"))
	if err != nil {
		t.Fatalf("read Level.sav: %v", err)
	}
	return strings.TrimPrefix(string(b), "LEVELDATA-")
}

func TestCreateListVerify(t *testing.T) {
	root := t.TempDir()
	world := filepath.Join(t.TempDir(), "8B68TESTGUID")
	makeWorld(t, world, "v1")
	m, err := NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	e, err := m.Create(context.Background(), world, TriggerManual)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if e.WorldName != "8B68TESTGUID" || e.TotalSize == 0 {
		t.Fatalf("bad entry: %+v", e)
	}
	entries, partials, err := m.List()
	if err != nil || len(entries) != 1 || len(partials) != 0 {
		t.Fatalf("List: %v entries=%d partials=%d", err, len(entries), len(partials))
	}
	if err := m.Verify(&entries[0]); err != nil {
		t.Fatalf("Verify fresh backup: %v", err)
	}
}

func TestVerifyDetectsTampering(t *testing.T) {
	root := t.TempDir()
	world := filepath.Join(t.TempDir(), "W")
	makeWorld(t, world, "v1")
	m, _ := NewManager(root)
	e, _ := m.Create(context.Background(), world, TriggerManual)

	// Truncate a file inside the backup: size mismatch must be caught.
	victim := filepath.Join(e.WorldPath(), "Level.sav")
	if err := os.WriteFile(victim, []byte("X"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := m.Verify(e); err == nil {
		t.Fatal("tampered backup must fail verification")
	}

	// A deleted file must be caught too.
	makeWorld(t, filepath.Join(e.Path, worldSubdir), "v1") // restore content
	os.Remove(filepath.Join(e.WorldPath(), "Players", "0001.sav"))
	if err := m.Verify(e); err == nil {
		t.Fatal("missing file must fail verification")
	}
}

func TestPartialsAreQuarantinedNotListed(t *testing.T) {
	root := t.TempDir()
	m, _ := NewManager(root)
	// Simulate a crash mid-backup.
	if err := os.MkdirAll(filepath.Join(root, partialPrefix+"20260101T000000Z-manual"), 0o750); err != nil {
		t.Fatal(err)
	}
	entries, partials, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 || len(partials) != 1 {
		t.Fatalf("partials must be reported separately: entries=%d partials=%d", len(entries), len(partials))
	}
	// Prune must never touch partials (crash evidence).
	if _, err := m.Prune(0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(partials[0]); err != nil {
		t.Fatal("prune deleted a partial — it must not")
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	root := t.TempDir()
	world := filepath.Join(t.TempDir(), "W")
	makeWorld(t, world, "v1")
	m, _ := NewManager(root)
	var ids []string
	for i := 0; i < 3; i++ {
		e, err := m.Create(context.Background(), world, TriggerScheduled)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, e.ID)
		time.Sleep(1100 * time.Millisecond) // distinct second-resolution IDs
	}
	deleted, err := m.Prune(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 1 || deleted[0] != ids[0] {
		t.Fatalf("prune should delete only the oldest (%s); deleted %v", ids[0], deleted)
	}
	entries, _, _ := m.List()
	if len(entries) != 2 {
		t.Fatalf("want 2 remaining, got %d", len(entries))
	}
}

// ---- restore payload through the real engine --------------------------------

type okAPI struct{}

func (okAPI) Announce(context.Context, string) error         { return nil }
func (okAPI) Save(context.Context) error                     { return nil }
func (okAPI) WaitReady(context.Context, time.Duration) error { return nil }

type okUnit struct{}

func (okUnit) Start(context.Context) error                      { return nil }
func (okUnit) Stop(context.Context) error                       { return nil }
func (okUnit) Kill(context.Context) error                       { return nil }
func (okUnit) WaitStopped(context.Context, time.Duration) error { return nil }

type okSusp struct{}

func (okSusp) Suspend() {}
func (okSusp) Resume()  {}

func engineFor(t *testing.T) *maintain.Engine {
	t.Helper()
	j, err := maintain.NewFileJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	eng, err := maintain.NewEngine(maintain.Config{
		API: okAPI{}, Unit: okUnit{}, Susp: okSusp{}, Journal: j,
	})
	if err != nil {
		t.Fatal(err)
	}
	return eng
}

func TestFullRestoreThroughEngine(t *testing.T) {
	saves := t.TempDir()
	world := filepath.Join(saves, "GUID123")
	makeWorld(t, world, "GOOD")
	m, _ := NewManager(t.TempDir())
	backup, err := m.Create(context.Background(), world, TriggerManual)
	if err != nil {
		t.Fatal(err)
	}

	// The world then gets "griefed".
	makeWorld(t, world, "GRIEFED")
	if readMarker(t, world) != "GRIEFED" {
		t.Fatal("setup failed")
	}

	p := &RestorePayload{
		Mgr: m, Selected: backup, WorldDir: world,
		ReadWorldGUID: func(context.Context) (string, error) { return "GUID123", nil },
	}
	out, err := engineFor(t).Run(context.Background(), "restore-1", p)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The safety-copy line is now an informational NOTE, so a clean
	// restore (identity matches) reports as SUCCESS, not warnings.
	if out.Status != maintain.StatusSuccess {
		t.Fatalf("want success, got %+v", out)
	}
	if len(out.VerifyNotes) == 0 {
		t.Fatal("restore should carry the informational safety-copy note")
	}
	if readMarker(t, world) != "GOOD" {
		t.Fatal("world was not restored from the backup")
	}
	// The griefed world survives as the safety copy (restore is reversible).
	if p.safetyPath == "" {
		t.Fatal("safety path not recorded")
	}
	if got := readMarker(t, p.safetyPath); got != "GRIEFED" {
		t.Fatalf("safety copy should hold the pre-restore world, got %q", got)
	}
}

func TestRestoreVerifyFlagsWrongWorldIdentity(t *testing.T) {
	saves := t.TempDir()
	world := filepath.Join(saves, "GUID123")
	makeWorld(t, world, "GOOD")
	m, _ := NewManager(t.TempDir())
	b, _ := m.Create(context.Background(), world, TriggerManual)
	p := &RestorePayload{Mgr: m, Selected: b, WorldDir: world,
		ReadWorldGUID: func(context.Context) (string, error) { return "DIFFERENT", nil }}
	out, err := engineFor(t).Run(context.Background(), "restore-2", p)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != maintain.StatusSuccessWithWarnings {
		t.Fatalf("want success_with_warnings, got %+v", out)
	}
	if !strings.Contains(strings.Join(out.VerifyIssues, "\n"), "world identity mismatch") {
		t.Fatalf("identity mismatch must be a warning: %v", out.VerifyIssues)
	}
}

func TestRestoreRollbackPutsOriginalBack(t *testing.T) {
	saves := t.TempDir()
	world := filepath.Join(saves, "GUID123")
	makeWorld(t, world, "ORIGINAL")
	m, _ := NewManager(t.TempDir())
	b, _ := m.Create(context.Background(), world, TriggerManual)
	p := &RestorePayload{Mgr: m, Selected: b, WorldDir: world,
		ReadWorldGUID: func(context.Context) (string, error) { return "GUID123", nil }}
	ctx := context.Background()
	if err := p.PreCheck(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Backup(ctx); err != nil { // rename-aside
		t.Fatal(err)
	}
	if _, err := os.Stat(world); !os.IsNotExist(err) {
		t.Fatal("rename-aside should have moved the world")
	}
	// Simulate a partial Apply, then roll back.
	os.MkdirAll(world, 0o750)
	os.WriteFile(filepath.Join(world, "Level.sav"), []byte("PARTIAL"), 0o640)
	if err := p.RollbackApply(ctx); err != nil {
		t.Fatalf("RollbackApply: %v", err)
	}
	if readMarker(t, world) != "ORIGINAL" {
		t.Fatal("rollback must restore the original world exactly")
	}
	if _, err := os.Stat(p.safetyPath); p.safetyPath != "" {
		_ = err
		t.Fatal("safety path should be cleared after rollback")
	}
}

func TestPreCheckRefusesCorruptBackupBeforeStopping(t *testing.T) {
	saves := t.TempDir()
	world := filepath.Join(saves, "G")
	makeWorld(t, world, "v1")
	m, _ := NewManager(t.TempDir())
	b, _ := m.Create(context.Background(), world, TriggerManual)
	os.Remove(filepath.Join(b.WorldPath(), "Level.sav")) // corrupt it
	p := &RestorePayload{Mgr: m, Selected: b, WorldDir: world,
		ReadWorldGUID: func(context.Context) (string, error) { return "G", nil }}
	if err := p.PreCheck(context.Background()); err == nil {
		t.Fatal("a corrupt backup must be refused BEFORE the server is stopped")
	}
}

func TestSafetyCopyRelocatesOutOfSaveTreeAndIsDotPrefixed(t *testing.T) {
	saves := t.TempDir() // stands in for SaveGames/0
	world := filepath.Join(saves, "GUID123")
	makeWorld(t, world, "ORIGINAL")
	m, _ := NewManager(t.TempDir())
	b, _ := m.Create(context.Background(), world, TriggerManual)
	makeWorld(t, world, "GRIEFED")

	relocateDir := t.TempDir() // Paladin's own root, outside the save tree
	p := &RestorePayload{
		Mgr: m, Selected: b, WorldDir: world, SafetyRelocateDir: relocateDir,
		ReadWorldGUID: func(context.Context) (string, error) { return "GUID123", nil },
	}
	out, err := engineFor(t).Run(context.Background(), "reloc-1", p)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != maintain.StatusSuccess {
		t.Fatalf("want success (note only), got %+v", out)
	}
	// The world is restored...
	if readMarker(t, world) != "ORIGINAL" {
		t.Fatal("world not restored")
	}
	// ...and the ONLY directory left in the save tree is the world itself.
	// This is the regression: before the fix, a .paladin-safety-* sibling
	// sat here and broke world detection ("found two worlds").
	des, _ := os.ReadDir(saves)
	var leftover []string
	for _, de := range des {
		if de.IsDir() {
			leftover = append(leftover, de.Name())
		}
	}
	if len(leftover) != 1 || leftover[0] != "GUID123" {
		t.Fatalf("save tree must contain only the world after restore; found %v", leftover)
	}
	// The safety copy now lives in Paladin's root, holding the griefed world.
	relDes, _ := os.ReadDir(relocateDir)
	if len(relDes) != 1 {
		t.Fatalf("safety copy should have relocated into Paladin's root; found %d entries", len(relDes))
	}
	if got := readMarker(t, filepath.Join(relocateDir, relDes[0].Name())); got != "GRIEFED" {
		t.Fatalf("relocated safety copy should hold the pre-restore world, got %q", got)
	}
}

func TestSafetyScratchIsDotPrefixedDuringCycle(t *testing.T) {
	saves := t.TempDir()
	world := filepath.Join(saves, "G")
	makeWorld(t, world, "v1")
	m, _ := NewManager(t.TempDir())
	b, _ := m.Create(context.Background(), world, TriggerManual)
	p := &RestorePayload{Mgr: m, Selected: b, WorldDir: world,
		ReadWorldGUID: func(context.Context) (string, error) { return "G", nil }}
	ctx := context.Background()
	if err := p.PreCheck(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.Backup(ctx); err != nil { // rename-aside
		t.Fatal(err)
	}
	// The scratch sibling must be dot-prefixed so a detector skipping
	// dotfiles won't mistake it for a world.
	if !strings.HasPrefix(filepath.Base(p.safetyPath), ".") {
		t.Fatalf("safety scratch must be dot-prefixed, got %s", filepath.Base(p.safetyPath))
	}
	if filepath.Dir(p.safetyPath) != saves {
		t.Fatal("safety scratch must be a same-parent sibling (atomicity)")
	}
}

func TestDiskCheck(t *testing.T) {
	world := filepath.Join(t.TempDir(), "W")
	makeWorld(t, world, "v1")
	if err := DiskCheckFunc(world, 2.0)(); err != nil {
		t.Fatalf("tiny world on a real filesystem should pass: %v", err)
	}
	// An absurd factor must fail (needs more than the disk holds).
	if err := DiskCheckFunc(world, 1e15)(); err == nil {
		t.Fatal("impossible requirement should fail the pre-flight")
	}
}
