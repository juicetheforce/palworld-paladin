package maintain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- fakes -----------------------------------------------------------------

type fakeAPI struct {
	log          *[]string
	failAnnounce bool
	failSave     bool
	notReady     bool // WaitReady fails (used for pre-check health)
	readyFails   int  // WaitReady fails this many times, then succeeds
}

func (f *fakeAPI) Announce(_ context.Context, msg string) error {
	*f.log = append(*f.log, "announce:"+msg)
	if f.failAnnounce {
		return errors.New("broadcast down")
	}
	return nil
}
func (f *fakeAPI) Save(context.Context) error {
	*f.log = append(*f.log, "save")
	if f.failSave {
		return errors.New("save failed")
	}
	return nil
}
func (f *fakeAPI) WaitReady(context.Context, time.Duration) error {
	*f.log = append(*f.log, "waitready")
	if f.notReady {
		return errors.New("rest down")
	}
	if f.readyFails > 0 {
		f.readyFails--
		return errors.New("not ready yet")
	}
	return nil
}

type fakeUnit struct {
	log        *[]string
	stopStuck  bool // WaitStopped fails until killed
	killed     bool
	startFails int // Start+ready fails this many times (via unit start err)
}

func (f *fakeUnit) Start(context.Context) error {
	*f.log = append(*f.log, "unit.start")
	if f.startFails > 0 {
		f.startFails--
		return errors.New("unit failed to start")
	}
	return nil
}
func (f *fakeUnit) Stop(context.Context) error {
	*f.log = append(*f.log, "unit.stop")
	return nil
}
func (f *fakeUnit) Kill(context.Context) error {
	*f.log = append(*f.log, "unit.KILL")
	f.killed = true
	return nil
}
func (f *fakeUnit) WaitStopped(ctx context.Context, _ time.Duration) error {
	*f.log = append(*f.log, "waitstopped")
	if f.stopStuck && !f.killed {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

type fakeSusp struct {
	suspends, resumes int
}

func (f *fakeSusp) Suspend() { f.suspends++ }
func (f *fakeSusp) Resume()  { f.resumes++ }

type memJournal struct {
	begun, closed bool
	steps         []string
	outcome       string
}

func (m *memJournal) Begin(string, string) error { m.begun = true; return nil }
func (m *memJournal) StepStarted(_, s string)    { m.steps = append(m.steps, "start:"+s) }
func (m *memJournal) StepOK(_, s string)         { m.steps = append(m.steps, "ok:"+s) }
func (m *memJournal) StepFailed(_, s, _ string)  { m.steps = append(m.steps, "fail:"+s) }
func (m *memJournal) Close(_, outcome, _ string) error {
	m.closed, m.outcome = true, outcome
	return nil
}

type fakePayload struct {
	log            *[]string
	failPreCheck   bool
	failBackup     bool
	failApply      bool
	failRollback   bool
	verifyWarnings []string
	verifyNotes    []string
}

func (p *fakePayload) Name() string { return "test" }
func (p *fakePayload) PreCheck(context.Context) error {
	*p.log = append(*p.log, "p.precheck")
	if p.failPreCheck {
		return errors.New("bad diff")
	}
	return nil
}
func (p *fakePayload) Backup(context.Context) error {
	*p.log = append(*p.log, "p.backup")
	if p.failBackup {
		return errors.New("copy failed (partials cleaned)")
	}
	return nil
}
func (p *fakePayload) Apply(context.Context) error {
	*p.log = append(*p.log, "p.apply")
	if p.failApply {
		return errors.New("write failed")
	}
	return nil
}
func (p *fakePayload) RollbackApply(context.Context) error {
	*p.log = append(*p.log, "p.rollback")
	if p.failRollback {
		return errors.New("rollback broke")
	}
	return nil
}
func (p *fakePayload) Verify(context.Context) (VerifyResult, error) {
	*p.log = append(*p.log, "p.verify")
	return VerifyResult{Warnings: p.verifyWarnings, Notes: p.verifyNotes}, nil
}
func (p *fakePayload) Anchors() []string { return []string{"/backups/pre", "/tmp/ini.orig"} }

// ---- harness ---------------------------------------------------------------

type rig struct {
	log     []string
	api     *fakeAPI
	unit    *fakeUnit
	susp    *fakeSusp
	journal *memJournal
	payload *fakePayload
	events  []Event
	cfg     Config
}

func newRig() *rig {
	r := &rig{}
	r.api = &fakeAPI{log: &r.log}
	r.unit = &fakeUnit{log: &r.log}
	r.susp = &fakeSusp{}
	r.journal = &memJournal{}
	r.payload = &fakePayload{log: &r.log}
	r.cfg = Config{
		API: r.api, Unit: r.unit, Susp: r.susp, Journal: r.journal,
		OnEvent:       func(e Event) { r.events = append(r.events, e) },
		Announcements: []Announcement{{Message: "maint in 0", Wait: 0}},
		StopGrace:     50 * time.Millisecond,
		KillGrace:     50 * time.Millisecond,
	}
	return r
}

func (r *rig) run(t *testing.T) Outcome {
	t.Helper()
	e, err := NewEngine(r.cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	out, err := e.Run(context.Background(), "c1", r.payload)
	if err != nil && !errors.Is(err, ErrBusy) {
		t.Fatalf("Run infra error: %v", err)
	}
	// Invariant I2: suspend/resume must bracket EVERY outcome.
	if r.susp.suspends != 1 || r.susp.resumes != 1 {
		t.Fatalf("I2 violated: suspends=%d resumes=%d (outcome %s)",
			r.susp.suspends, r.susp.resumes, out.Status)
	}
	// Invariant I3: journal opened and closed on every outcome.
	if !r.journal.begun || !r.journal.closed {
		t.Fatalf("I3 violated: begun=%v closed=%v", r.journal.begun, r.journal.closed)
	}
	if r.journal.outcome != string(out.Status) {
		t.Fatalf("journal outcome %q != %q", r.journal.outcome, out.Status)
	}
	return out
}

func (r *rig) sawCall(s string) bool {
	for _, c := range r.log {
		if c == s {
			return true
		}
	}
	return false
}

// ---- the matrix, row by row ------------------------------------------------

func TestHappyPath(t *testing.T) {
	r := newRig()
	out := r.run(t)
	if out.Status != StatusSuccess {
		t.Fatalf("want success, got %+v", out)
	}
	want := []string{"waitready", "p.precheck", "announce:maint in 0", "save",
		"unit.stop", "waitstopped", "p.backup", "p.apply", "unit.start",
		"waitready", "p.verify"}
	got := strings.Join(r.log, " → ")
	if got != strings.Join(want, " → ") {
		t.Fatalf("sequence:\n got  %s\n want %s", got, strings.Join(want, " → "))
	}
}

func TestPreCheckFailTouchesNothing(t *testing.T) {
	r := newRig()
	r.payload.failPreCheck = true
	out := r.run(t)
	if out.Status != StatusAborted || out.FailedStep != StepPreCheck {
		t.Fatalf("want aborted@precheck, got %+v", out)
	}
	for _, forbidden := range []string{"save", "unit.stop", "p.backup", "p.apply"} {
		if r.sawCall(forbidden) {
			t.Fatalf("aborted pre-check must touch nothing; saw %q", forbidden)
		}
	}
}

func TestUnhealthyServerAborts(t *testing.T) {
	r := newRig()
	r.api.notReady = true
	out := r.run(t)
	if out.Status != StatusAborted || out.FailedStep != StepPreCheck {
		t.Fatalf("want aborted@precheck, got %+v", out)
	}
}

func TestAnnounceFailAbortsByDefault(t *testing.T) {
	r := newRig()
	r.api.failAnnounce = true
	out := r.run(t)
	if out.Status != StatusAborted || out.FailedStep != StepAnnounce {
		t.Fatalf("want aborted@announce, got %+v", out)
	}
	if r.sawCall("unit.stop") {
		t.Fatal("must not stop after failed announce")
	}
}

func TestAnnounceFailOverride(t *testing.T) {
	r := newRig()
	r.api.failAnnounce = true
	r.cfg.ProceedIfAnnounceFails = true
	out := r.run(t)
	if out.Status != StatusSuccess {
		t.Fatalf("override should proceed to success, got %+v", out)
	}
}

func TestSaveFailAborts(t *testing.T) {
	r := newRig()
	r.api.failSave = true
	out := r.run(t)
	if out.Status != StatusAborted || out.FailedStep != StepSave {
		t.Fatalf("want aborted@save, got %+v", out)
	}
	if r.sawCall("unit.stop") {
		t.Fatal("never stop on an unsaved world")
	}
}

func TestStopStuckDefaultsToCancelNeverKills(t *testing.T) {
	r := newRig()
	r.unit.stopStuck = true
	// No StopDecider configured: safe default is Cancel.
	out := r.run(t)
	if out.Status != StatusAborted || out.FailedStep != StepStop {
		t.Fatalf("want aborted@stop, got %+v", out)
	}
	if r.unit.killed {
		t.Fatal("engine must NEVER kill without an explicit operator decision")
	}
	if r.sawCall("p.backup") {
		t.Fatal("cancelled cycle must not proceed")
	}
}

func TestStopStuckOperatorCancels(t *testing.T) {
	r := newRig()
	r.unit.stopStuck = true
	r.cfg.StopDecider = func(context.Context) (StopDecision, error) { return DecisionCancel, nil }
	out := r.run(t)
	if out.Status != StatusAborted || r.unit.killed {
		t.Fatalf("cancel must abort without killing: %+v killed=%v", out, r.unit.killed)
	}
}

func TestStopStuckOperatorForceKillsAndContinues(t *testing.T) {
	r := newRig()
	r.unit.stopStuck = true
	r.cfg.StopDecider = func(context.Context) (StopDecision, error) { return DecisionForceKill, nil }
	out := r.run(t)
	if out.Status != StatusSuccess {
		t.Fatalf("force-kill path should complete the cycle, got %+v", out)
	}
	if !r.unit.killed {
		t.Fatal("expected SIGKILL after explicit operator decision")
	}
	if !r.sawCall("p.backup") || !r.sawCall("p.apply") {
		t.Fatal("cycle should resume from BACKUP after kill")
	}
	// The dialog must have been surfaced.
	var awaited bool
	for _, e := range r.events {
		if e.Kind == EventAwaitingOp {
			awaited = true
		}
	}
	if !awaited {
		t.Fatal("EventAwaitingOp must be emitted before any kill")
	}
}

func TestBackupFailRestartsUnchangedWorld(t *testing.T) {
	r := newRig()
	r.payload.failBackup = true
	out := r.run(t)
	if out.Status != StatusFailedRecovered || out.FailedStep != StepBackup {
		t.Fatalf("want failed_recovered@backup, got %+v", out)
	}
	if r.sawCall("p.apply") || r.sawCall("p.rollback") {
		t.Fatal("backup failure: nothing was applied, nothing to roll back")
	}
	if !r.sawCall("unit.start") {
		t.Fatal("pivot rule: must restart the unchanged world")
	}
	if len(out.Anchors) == 0 {
		t.Fatal("I7: anchors must be named on failure outcomes")
	}
}

func TestApplyFailRollsBackAndRestarts(t *testing.T) {
	r := newRig()
	r.payload.failApply = true
	out := r.run(t)
	if out.Status != StatusFailedRecovered || out.FailedStep != StepApply {
		t.Fatalf("want failed_recovered@apply, got %+v", out)
	}
	if !r.sawCall("p.rollback") || !r.sawCall("unit.start") {
		t.Fatalf("apply failure must rollback then restart; log=%v", r.log)
	}
}

func TestStartFailGetsExactlyOneRollback(t *testing.T) {
	r := newRig()
	r.unit.startFails = 1 // first start fails, post-rollback start succeeds
	out := r.run(t)
	if out.Status != StatusFailedRecovered || out.FailedStep != StepStart {
		t.Fatalf("want failed_recovered@start, got %+v", out)
	}
	rollbacks := 0
	for _, c := range r.log {
		if c == "p.rollback" {
			rollbacks++
		}
	}
	if rollbacks != 1 {
		t.Fatalf("exactly one automatic rollback allowed; got %d", rollbacks)
	}
}

func TestStartFailTwiceHaltsDown(t *testing.T) {
	r := newRig()
	r.unit.startFails = 2
	out := r.run(t)
	if out.Status != StatusHaltedDown || out.FailedStep != StepStart {
		t.Fatalf("want halted_down@start, got %+v", out)
	}
	if len(out.Anchors) != 2 {
		t.Fatalf("I7: halted outcome must name anchors, got %v", out.Anchors)
	}
	starts := 0
	for _, c := range r.log {
		if c == "unit.start" {
			starts++
		}
	}
	if starts != 2 {
		t.Fatalf("no restart loops: want exactly 2 start attempts, got %d", starts)
	}
}

func TestRollbackFailureIsDoubleFaultHalt(t *testing.T) {
	r := newRig()
	r.payload.failApply = true
	r.payload.failRollback = true
	out := r.run(t)
	if out.Status != StatusHaltedDown {
		t.Fatalf("double fault must halt; got %+v", out)
	}
	if r.sawCall("unit.start") {
		t.Fatal("after a double fault, ALL automation stops — no recovery start")
	}
	var df bool
	for _, e := range r.events {
		if e.Kind == EventDoubleFault {
			df = true
		}
	}
	if !df {
		t.Fatal("EventDoubleFault must be emitted")
	}
}

func TestVerifyIssuesReportedNeverRolledBack(t *testing.T) {
	r := newRig()
	r.payload.verifyWarnings = []string{"BaseCampMaxNumInGuild: applied but level-gated"}
	out := r.run(t)
	if out.Status != StatusSuccessWithWarnings {
		t.Fatalf("want success_with_warnings, got %+v", out)
	}
	if r.sawCall("p.rollback") {
		t.Fatal("VERIFY issues are reported, NEVER auto-rolled-back")
	}
	if len(out.VerifyIssues) != 1 {
		t.Fatalf("issues must surface in the outcome: %+v", out)
	}
}

func TestSingleFlight(t *testing.T) {
	r := newRig()
	e, _ := NewEngine(r.cfg)
	blocked := make(chan struct{})
	release := make(chan struct{})
	slow := &fakePayload{log: &r.log}
	go func() {
		e.Run(context.Background(), "long", &blockingPayload{fakePayload: slow, gate: release, entered: blocked})
	}()
	<-blocked
	_, err := e.Run(context.Background(), "second", &fakePayload{log: &r.log})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("I1: want ErrBusy, got %v", err)
	}
	close(release)
}

type blockingPayload struct {
	*fakePayload
	gate    chan struct{}
	entered chan struct{}
}

func (b *blockingPayload) PreCheck(ctx context.Context) error {
	close(b.entered)
	<-b.gate
	return b.fakePayload.PreCheck(ctx)
}

// ---- journal ----------------------------------------------------------------

func TestFileJournalLifecycleAndUnclosedDetection(t *testing.T) {
	dir := t.TempDir()
	j, err := NewFileJournal(dir)
	if err != nil {
		t.Fatalf("NewFileJournal: %v", err)
	}
	if err := j.Begin("cycle-1", "commit"); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	j.StepStarted("cycle-1", "SAVE")
	j.StepOK("cycle-1", "SAVE")
	j.StepStarted("cycle-1", "APPLY")
	// Simulate a crash here: no Close. A fresh process inspects the dir.
	u, err := ReadUnclosed(dir)
	if err != nil {
		t.Fatalf("ReadUnclosed: %v", err)
	}
	if u == nil || u.CycleID != "cycle-1" || u.Kind != "commit" || u.LastStep != "APPLY" {
		t.Fatalf("bad crash report: %+v", u)
	}

	// A new cycle must refuse to start over an unclosed journal.
	j2, _ := NewFileJournal(dir)
	if err := j2.Begin("cycle-2", "commit"); err == nil {
		t.Fatal("Begin must refuse while an unclosed journal exists")
	}

	// Close the original properly; detection clears; archive exists.
	if err := j.Close("cycle-1", "success", ""); err != nil {
		t.Fatalf("Close: %v", err)
	}
	u, err = ReadUnclosed(dir)
	if err != nil || u != nil {
		t.Fatalf("expected clean state, got %+v err=%v", u, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cycle-1.journal")); err != nil {
		t.Fatalf("archived journal missing: %v", err)
	}
}

func TestFileJournalSurvivesTornFinalLine(t *testing.T) {
	dir := t.TempDir()
	j, _ := NewFileJournal(dir)
	j.Begin("c", "restore")
	j.StepStarted("c", "APPLY")
	// Simulate a torn write from a crash mid-line.
	f, _ := os.OpenFile(filepath.Join(dir, "active.journal"), os.O_APPEND|os.O_WRONLY, 0)
	fmt.Fprint(f, `{"time":"2026-07-27T`) // truncated JSON
	f.Close()
	u, err := ReadUnclosed(dir)
	if err != nil {
		t.Fatalf("torn line must not break recovery: %v", err)
	}
	if u.LastStep != "APPLY" {
		t.Fatalf("recovery should still know the last step: %+v", u)
	}
}
