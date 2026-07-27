//go:build integration

// Read-only integration checks against the test box's live unit.
// No root needed (systemctl show/is-active are unprivileged), and nothing
// here starts, stops, or restarts anything.
//
// Run on the test box:
//
//	go test -tags=integration -v ./internal/supervise/
//
// Optional: PALADIN_TEST_UNIT (default palserver.service).
package supervise

import (
	"context"
	"os"
	"testing"
	"time"
)

func liveUnit(t *testing.T) *UnitController {
	t.Helper()
	unit := os.Getenv("PALADIN_TEST_UNIT")
	if unit == "" {
		unit = "palserver.service"
	}
	return NewUnitController(unit)
}

func TestLiveShow(t *testing.T) {
	u := liveUnit(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	p, err := u.Show(ctx)
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	t.Logf("unit %s: ActiveState=%s SubState=%s MainPID=%d MemoryCurrent=%d bytes (%.1f MiB)",
		u.Unit, p.ActiveState, p.SubState, p.MainPID, p.MemoryCurrent,
		float64(p.MemoryCurrent)/(1<<20))
	if p.ActiveState != "active" {
		t.Fatalf("expected the test server to be running; ActiveState=%s", p.ActiveState)
	}
	if p.MainPID == 0 {
		t.Fatal("active unit with MainPID=0 is suspicious")
	}
	// The whole point of cgroup-based memory: a live Palworld server is
	// hundreds of MiB, while MainPID (the shell script) is a few MiB.
	// If this reads tiny, we're measuring the wrong thing.
	if p.MemoryCurrent < 100<<20 {
		t.Fatalf("MemoryCurrent=%d bytes — too small for a live Palworld server; wrong source?",
			p.MemoryCurrent)
	}
}

func TestLiveIsActive(t *testing.T) {
	u := liveUnit(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	active, err := u.IsActive(ctx)
	if err != nil {
		t.Fatalf("IsActive: %v", err)
	}
	if !active {
		t.Fatal("test server should be active")
	}
}
