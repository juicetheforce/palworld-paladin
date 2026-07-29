package update

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/juicetheforce/palworld-paladin/internal/maintain"
)

// deps returns a Deps where an update IS available (local != remote).
func testDeps() (Deps, *recorder) {
	rec := &recorder{}
	return Deps{
		LocalBuildID: func() (string, error) {
			rec.localReads++
			if rec.updated {
				return "222", nil
			}
			return "111", nil
		},
		RemoteBuildID: func(context.Context) (string, error) { return "222", nil },
		RunUpdate: func(_ context.Context, onLine func(string)) error {
			rec.ran = true
			rec.updated = true
			if onLine != nil {
				onLine("Update state (0x61) downloading, progress: 42.0")
				onLine("Success! App '2394010' fully installed.")
			}
			return nil
		},
		GameVersion: func(context.Context) (string, error) {
			if rec.updated {
				return "v1.0.2", nil
			}
			return "v1.0.1", nil
		},
		Backup: func(context.Context) (string, error) { rec.backedUp = true; return "/backups/pre-update-x", nil },
		OnLine: nil,
	}, rec
}

type recorder struct {
	localReads int
	ran        bool
	updated    bool
	backedUp   bool
}

func TestUpToDateAbortsBeforeAnythingTouched(t *testing.T) {
	d, rec := testDeps()
	d.RemoteBuildID = func(context.Context) (string, error) { return "111", nil } // same as local
	p, _ := New(d)
	err := p.PreCheck(context.Background())
	if !errors.Is(err, ErrUpToDate) {
		t.Fatalf("want ErrUpToDate, got %v", err)
	}
	if !p.UpToDate {
		t.Fatal("UpToDate flag must be set")
	}
	if rec.ran || rec.backedUp {
		t.Fatal("nothing must run when already up to date")
	}
}

func TestPreCheckCapturesStateWhenUpdateAvailable(t *testing.T) {
	d, _ := testDeps()
	p, _ := New(d)
	if err := p.PreCheck(context.Background()); err != nil {
		t.Fatalf("PreCheck with update available must pass: %v", err)
	}
	if p.UpToDate {
		t.Fatal("UpToDate must be false when buildids differ")
	}
	if p.localBefore != "111" || p.remote != "222" || p.versionBefore != "v1.0.1" {
		t.Fatalf("state not captured: %+v", p)
	}
}

func TestVerifyReportsVersionAndBuildid(t *testing.T) {
	d, _ := testDeps()
	p, _ := New(d)
	p.PreCheck(context.Background())
	p.Backup(context.Background())
	p.Apply(context.Background())
	res, err := p.Verify(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("clean update should have no warnings: %v", res.Warnings)
	}
	joined := strings.Join(res.Notes, " | ")
	for _, want := range []string{"111 → 222", "v1.0.1 → v1.0.2", "pre-update world backup"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("notes missing %q: %v", want, res.Notes)
		}
	}
}

func TestVerifyWarnsOnBuildidMismatch(t *testing.T) {
	d, rec := testDeps()
	// Sabotage: RunUpdate claims success but the manifest never changes.
	d.RunUpdate = func(context.Context, func(string)) error { rec.ran = true; return nil }
	p, _ := New(d)
	p.PreCheck(context.Background())
	p.Apply(context.Background())
	res, _ := p.Verify(context.Background())
	if len(res.Warnings) == 0 {
		t.Fatal("buildid mismatch after update must warn")
	}
}

// ---- full cycle through the real engine ----

type engAPI struct {
	announced []string
	saved     bool
}

func (a *engAPI) Announce(_ context.Context, m string) error {
	a.announced = append(a.announced, m)
	return nil
}
func (a *engAPI) Save(context.Context) error                     { a.saved = true; return nil }
func (a *engAPI) WaitReady(context.Context, time.Duration) error { return nil }

type engUnit struct{ stopped, started bool }

func (u *engUnit) Start(context.Context) error                      { u.started = true; return nil }
func (u *engUnit) Stop(context.Context) error                       { u.stopped = true; return nil }
func (u *engUnit) Kill(context.Context) error                       { return nil }
func (u *engUnit) WaitStopped(context.Context, time.Duration) error { return nil }

type engSusp struct{ suspended, resumed bool }

func (s *engSusp) Suspend() { s.suspended = true }
func (s *engSusp) Resume()  { s.resumed = true }

func TestFullUpdateCycleThroughEngine(t *testing.T) {
	api := &engAPI{}
	unit := &engUnit{}
	susp := &engSusp{}
	j, err := maintain.NewFileJournal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	eng, err := maintain.NewEngine(maintain.Config{
		API: api, Unit: unit, Susp: susp, Journal: j,
		Announcements: []maintain.Announcement{{Message: "updating soon", Wait: 0}},
	})
	if err != nil {
		t.Fatal(err)
	}

	d, rec := testDeps()
	p, _ := New(d)
	out, err := eng.Run(context.Background(), "cycle-test-update", p)
	if err != nil {
		t.Fatalf("engine error: %v", err)
	}
	if out.Status != maintain.StatusSuccess {
		t.Fatalf("want success, got %s: %s", out.Status, out.Detail)
	}
	if !rec.backedUp || !rec.ran || !api.saved || !unit.stopped || !unit.started {
		t.Fatalf("cycle steps incomplete: rec=%+v api=%+v unit=%+v", rec, api, unit)
	}
	if !susp.suspended || !susp.resumed {
		t.Fatal("supervision must be suspended and resumed (I2)")
	}
}

func TestUpToDateCycleThroughEngineIsCleanAbort(t *testing.T) {
	api := &engAPI{}
	unit := &engUnit{}
	j, _ := maintain.NewFileJournal(t.TempDir())
	eng, _ := maintain.NewEngine(maintain.Config{API: api, Unit: unit, Susp: &engSusp{}, Journal: j})

	d, rec := testDeps()
	d.RemoteBuildID = func(context.Context) (string, error) { return "111", nil }
	p, _ := New(d)
	out, _ := eng.Run(context.Background(), "cycle-test-uptodate", p)
	if out.Status != maintain.StatusAborted {
		t.Fatalf("up-to-date must abort cleanly, got %s", out.Status)
	}
	if !p.UpToDate {
		t.Fatal("UpToDate flag must be readable after the cycle")
	}
	if unit.stopped || rec.backedUp || rec.ran {
		t.Fatal("an up-to-date server must never be touched")
	}
}
