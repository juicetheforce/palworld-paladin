package maintain

import (
	"context"
	"time"
)

// Step names the stations of the shared maintenance state machine
// (DESIGN.md §6.3 / §6.8 / §6.9). One engine, two payloads.
type Step string

const (
	StepPreCheck Step = "PRE-CHECK"
	StepAnnounce Step = "ANNOUNCE"
	StepSave     Step = "SAVE"
	StepStop     Step = "STOP"
	StepBackup   Step = "BACKUP"
	StepApply    Step = "APPLY"
	StepStart    Step = "START"
	StepVerify   Step = "VERIFY"
)

// Payload is what differs between the two workflows that share the
// engine: settings commit and world restore. The engine owns every
// invariant; the payload owns only the difference (§6.9).
type Payload interface {
	// Name labels the cycle for the journal and audit log
	// ("commit" | "restore").
	Name() string
	// PreCheck validates the payload-specific preconditions: staged-diff
	// types/ranges for a commit, backup-archive integrity for a restore.
	// Runs while the server is still up; failure aborts untouched.
	PreCheck(ctx context.Context) error
	// Backup runs with the server confirmed stopped: pre-commit world
	// copy for a commit, rename-aside safety backup for a restore. On
	// error it MUST have cleaned up its own partials before returning
	// (the engine then restarts the unchanged world).
	Backup(ctx context.Context) error
	// Apply performs the mutation: ini write (after taking the pre-write
	// copy) or world swap-in. Invariant I5: nothing Apply overwrites may
	// lack a preserved prior copy.
	Apply(ctx context.Context) error
	// RollbackApply restores the pre-Apply state (pre-write ini copy /
	// safety backup back in place). Called at most once per cycle; a
	// failure here is a double fault and halts all automation.
	RollbackApply(ctx context.Context) error
	// Verify runs after a healthy START: settings readback for a commit,
	// world-identity check for a restore. Results are reported, never
	// auto-rolled-back (§6.9 matrix, VERIFY rows). Warnings and Notes are
	// distinct: a Warning is something the operator may need to act on (a
	// value didn't take, an override may apply); a Note is purely
	// informational (where the safety copy landed). A clean cycle with
	// only Notes is a SUCCESS, not a warning state.
	Verify(ctx context.Context) (VerifyResult, error)
	// Anchors returns the absolute paths of the recovery anchors for
	// failure reports (invariant I7).
	Anchors() []string
}

// VerifyResult separates actionable warnings from informational notes so
// a healthy cycle whose only output is an FYI (e.g. "safety copy is at X")
// reports as clean success rather than a scary "VERIFY FAILED".
type VerifyResult struct {
	// Warnings: the operator may need to act (value didn't take, override
	// may apply, identity mismatch). Non-empty → SuccessWithWarnings.
	Warnings []string
	// Notes: informational only (safety-copy location, applied context).
	// Never affect the outcome status.
	Notes []string
}

// GameAPI is the slice of the Palworld REST client the engine needs.
// *palapi.Client satisfies it.
type GameAPI interface {
	Announce(ctx context.Context, message string) error
	Save(ctx context.Context) error
	WaitReady(ctx context.Context, interval time.Duration) error
}

// Unit is the slice of the systemd controller the engine needs.
// *supervise.UnitController satisfies it.
type Unit interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Kill(ctx context.Context) error
	WaitStopped(ctx context.Context, interval time.Duration) error
}

// Suspender is the supervision gate (invariant I2).
// *supervise.Supervisor satisfies it.
type Suspender interface {
	Suspend()
	Resume()
}

// StopDecision is the operator's answer to the §6.9 STOP escalation
// dialog. There is no third option and no timeout-chosen default.
type StopDecision int

const (
	// DecisionCancel aborts the entire cycle; nothing on disk was
	// touched, the server is left in whatever state it is in.
	DecisionCancel StopDecision = iota
	// DecisionForceKill SIGKILLs the unit and resumes the cycle from
	// BACKUP as normal (the world was already saved at SAVE).
	DecisionForceKill
)

// Announcement is one broadcast in the pre-maintenance countdown.
// The engine sends Message, then waits Wait before the next action.
type Announcement struct {
	Message string
	Wait    time.Duration
}

// Config wires the engine's dependencies and timeouts (invariant I6).
type Config struct {
	API  GameAPI
	Unit Unit
	Susp Suspender
	// Journal records intent and progress (invariant I3). Required.
	Journal Journal
	// OnEvent receives live step progress for the UI and audit trail
	// (invariant I8). Optional.
	OnEvent func(Event)
	// Announcements to broadcast before stopping. Empty = no countdown.
	Announcements []Announcement
	// ProceedIfAnnounceFails: the explicit operator override from the
	// §6.9 matrix ANNOUNCE row. Default false: announce failure aborts,
	// players deserve warning.
	ProceedIfAnnounceFails bool
	// DiskCheck implements the invariant-I4 pre-flight (≥ ~2× world
	// size free). Nil skips the check (tests / callers that pre-check).
	DiskCheck func() error
	// UnitActive reports whether the server unit is currently active
	// (systemd ActiveState). Only consulted by RunOpts.TolerateStopped
	// cycles when the REST health probe fails, to distinguish "genuinely
	// stopped" (offline cycle proceeds) from "wedged" (abort). Optional;
	// nil means TolerateStopped cycles abort on an unreachable server.
	UnitActive func(ctx context.Context) (bool, error)
	// StopDecider is consulted only when the server misses StopGrace.
	// In the app this blocks on the two-option UI dialog; nil is treated
	// as DecisionCancel (the safe default — never kill without a human).
	StopDecider func(ctx context.Context) (StopDecision, error)

	// Timeouts. Zero values take the defaults noted.
	PreCheckTimeout time.Duration // 15s — server-health probe
	SaveTimeout     time.Duration // 60s
	StopGrace       time.Duration // 90s — before the escalation dialog
	KillGrace       time.Duration // 15s — after a user-ordered SIGKILL
	StartTimeout    time.Duration // 300s — genuine REST readiness
}

func (c *Config) defaults() {
	if c.PreCheckTimeout <= 0 {
		c.PreCheckTimeout = 15 * time.Second
	}
	if c.SaveTimeout <= 0 {
		c.SaveTimeout = 60 * time.Second
	}
	if c.StopGrace <= 0 {
		c.StopGrace = 90 * time.Second
	}
	if c.KillGrace <= 0 {
		c.KillGrace = 15 * time.Second
	}
	if c.StartTimeout <= 0 {
		c.StartTimeout = 300 * time.Second
	}
}

// Status is the honest final state of a cycle. Not a boolean on purpose.
type Status string

const (
	// StatusSuccess: every step completed, VERIFY clean.
	StatusSuccess Status = "success"
	// StatusSuccessWithWarnings: applied and running; VERIFY reported
	// issues (reported, never auto-rolled-back).
	StatusSuccessWithWarnings Status = "success_with_warnings"
	// StatusAborted: failed before anything was touched (or the operator
	// cancelled at the STOP dialog). Disk unchanged.
	StatusAborted Status = "aborted"
	// StatusFailedRecovered: a post-STOP step failed; the engine put
	// everything back and the server is RUNNING on its prior state.
	StatusFailedRecovered Status = "failed_recovered"
	// StatusHaltedDown: double fault — automation stopped, the server is
	// DOWN, anchors are named, an operator is required (§6.9).
	StatusHaltedDown Status = "halted_down"
)

// Outcome is the result of a cycle.
type Outcome struct {
	Status       Status
	FailedStep   Step     // zero for clean success
	Detail       string   // human-readable cause
	VerifyIssues []string // actionable warnings (populated for SuccessWithWarnings)
	VerifyNotes  []string // informational notes (present on any successful cycle)
	Anchors      []string // named on failure outcomes (invariant I7)
}

// EventKind labels progress events.
type EventKind string

const (
	EventStepStarted   EventKind = "step_started"
	EventStepOK        EventKind = "step_ok"
	EventStepFailed    EventKind = "step_failed"
	EventRolledBack    EventKind = "rolled_back"
	EventAwaitingOp    EventKind = "awaiting_operator" // STOP dialog shown
	EventCycleFinished EventKind = "cycle_finished"
	EventDoubleFault   EventKind = "double_fault"
)

// Event is one live progress notification (invariant I8).
type Event struct {
	Time    time.Time
	CycleID string
	Payload string // "commit" | "restore"
	Kind    EventKind
	Step    Step
	Detail  string
}
