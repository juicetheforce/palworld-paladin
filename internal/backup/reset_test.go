package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/juicetheforce/palworld-paladin/internal/maintain"
)

// A regenerated Palworld world REUSES the folder name (Palworld derives it
// from DedicatedServerName), so verify must judge freshness by file times,
// never by GUID difference — the first version of this check failed a
// perfectly good reset on the test box.
func TestResetVerifyAcceptsSameGUIDWhenWorldIsFresh(t *testing.T) {
	world := filepath.Join(t.TempDir(), "ABC123")
	if err := os.MkdirAll(world, 0o755); err != nil {
		t.Fatal(err)
	}
	p := &ResetPayload{
		WorldDir:      world,
		ReadWorldGUID: func(context.Context) (string, error) { return "ABC123", nil },
		ForceSave:     func(context.Context) error { return nil },
	}
	p.oldGUID = "ABC123"
	p.startedAt = time.Now().Add(-time.Minute)

	// Fresh world written after the cycle started.
	if err := os.WriteFile(filepath.Join(world, "Level.sav"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("same GUID with fresh files must NOT warn: %v", res.Warnings)
	}

	// Stale world (predating the cycle) must warn.
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(world, "Level.sav"), old, old); err != nil {
		t.Fatal(err)
	}
	res2, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Warnings) == 0 {
		t.Fatal("world files predating the reset must warn")
	}
}

var _ maintain.Payload = (*ResetPayload)(nil)
