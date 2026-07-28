package maintain

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Engine runs maintenance cycles. One engine per managed server;
// invariant I1 (single-flight) is enforced here with a try-lock.
type Engine struct {
	cfg Config
	mu  sync.Mutex // held for the duration of a cycle
}

// NewEngine validates config and returns an Engine.
func NewEngine(cfg Config) (*Engine, error) {
	if cfg.API == nil || cfg.Unit == nil || cfg.Susp == nil || cfg.Journal == nil {
		return nil, fmt.Errorf("maintain: API, Unit, Susp and Journal are required")
	}
	cfg.defaults()
	return &Engine{cfg: cfg}, nil
}

// ErrBusy: another maintenance cycle is already running (invariant I1).
var ErrBusy = fmt.Errorf("maintain: a maintenance cycle is already running")

// Run executes one full cycle for the given payload. It always returns an
// Outcome describing the honest final state; the error is non-nil only
// for infrastructure problems in the engine itself (e.g. journal I/O).
func (e *Engine) Run(ctx context.Context, cycleID string, p Payload) (Outcome, error) {
	if !e.mu.TryLock() {
		return Outcome{Status: StatusAborted, Detail: ErrBusy.Error()}, ErrBusy
	}
	defer e.mu.Unlock()

	if err := e.cfg.Journal.Begin(cycleID, p.Name()); err != nil {
		return Outcome{Status: StatusAborted, Detail: "journal begin failed: " + err.Error()},
			fmt.Errorf("maintain: journal: %w", err)
	}

	// Invariant I2: supervision is suspended for the WHOLE cycle and
	// resumed on every exit path, success or catastrophe.
	e.cfg.Susp.Suspend()
	defer e.cfg.Susp.Resume()

	out := e.run(ctx, cycleID, p)

	if err := e.cfg.Journal.Close(cycleID, string(out.Status), out.Detail); err != nil {
		// The cycle result stands; report the journal problem alongside.
		return out, fmt.Errorf("maintain: journal close: %w", err)
	}
	e.emit(cycleID, p, EventCycleFinished, "", string(out.Status)+": "+out.Detail)
	return out, nil
}

func (e *Engine) emit(id string, p Payload, kind EventKind, step Step, detail string) {
	if e.cfg.OnEvent != nil {
		e.cfg.OnEvent(Event{Time: time.Now(), CycleID: id, Payload: p.Name(),
			Kind: kind, Step: step, Detail: detail})
	}
}

// step journals + emits around fn. Returns fn's error.
func (e *Engine) step(ctx context.Context, id string, p Payload, s Step,
	fn func(context.Context) error) error {
	e.cfg.Journal.StepStarted(id, string(s))
	e.emit(id, p, EventStepStarted, s, "")
	err := fn(ctx)
	if err != nil {
		e.cfg.Journal.StepFailed(id, string(s), err.Error())
		e.emit(id, p, EventStepFailed, s, err.Error())
		return err
	}
	e.cfg.Journal.StepOK(id, string(s))
	e.emit(id, p, EventStepOK, s, "")
	return nil
}

func (e *Engine) run(ctx context.Context, id string, p Payload) Outcome {
	abort := func(s Step, err error) Outcome {
		return Outcome{Status: StatusAborted, FailedStep: s, Detail: err.Error()}
	}

	// ---- PRE-CHECK: server healthy, disk pre-flight, payload checks ----
	if err := e.step(ctx, id, p, StepPreCheck, func(c context.Context) error {
		hc, cancel := context.WithTimeout(c, e.cfg.PreCheckTimeout)
		defer cancel()
		if err := e.cfg.API.WaitReady(hc, time.Second); err != nil {
			return fmt.Errorf("server not healthy: %w", err)
		}
		if e.cfg.DiskCheck != nil {
			if err := e.cfg.DiskCheck(); err != nil {
				return fmt.Errorf("disk pre-flight (I4): %w", err)
			}
		}
		return p.PreCheck(c)
	}); err != nil {
		return abort(StepPreCheck, err)
	}

	// ---- ANNOUNCE: countdown broadcasts ----
	if err := e.step(ctx, id, p, StepAnnounce, func(c context.Context) error {
		for _, a := range e.cfg.Announcements {
			if err := e.cfg.API.Announce(c, a.Message); err != nil {
				if e.cfg.ProceedIfAnnounceFails {
					continue // explicit operator override (§6.9 matrix)
				}
				return fmt.Errorf("broadcast failed (players deserve warning): %w", err)
			}
			if a.Wait > 0 {
				select {
				case <-c.Done():
					return c.Err()
				case <-time.After(a.Wait):
				}
			}
		}
		return nil
	}); err != nil {
		return abort(StepAnnounce, err)
	}

	// ---- SAVE: never stop on an unsaved world ----
	if err := e.step(ctx, id, p, StepSave, func(c context.Context) error {
		sc, cancel := context.WithTimeout(c, e.cfg.SaveTimeout)
		defer cancel()
		return e.cfg.API.Save(sc)
	}); err != nil {
		return abort(StepSave, err)
	}

	// ---- STOP: graceful, confirmed; escalation is user-initiated only ----
	if err := e.step(ctx, id, p, StepStop, func(c context.Context) error {
		if err := e.cfg.Unit.Stop(c); err != nil {
			return fmt.Errorf("stop command failed: %w", err)
		}
		gc, cancel := context.WithTimeout(c, e.cfg.StopGrace)
		err := e.cfg.Unit.WaitStopped(gc, time.Second)
		cancel()
		if err == nil {
			return nil
		}
		// Grace window missed → the §6.9 two-option dialog. No timeout
		// default, no auto-kill: nil decider means Cancel.
		e.emit(id, p, EventAwaitingOp, StepStop,
			"server did not stop in grace window; awaiting operator decision")
		decision := DecisionCancel
		if e.cfg.StopDecider != nil {
			d, derr := e.cfg.StopDecider(c)
			if derr != nil {
				return fmt.Errorf("stop escalation: decision unavailable (%v); cancelling", derr)
			}
			decision = d
		}
		if decision == DecisionCancel {
			return fmt.Errorf("operator cancelled at stop escalation; server left as-is (may be hung)")
		}
		// DecisionForceKill: world was already saved at SAVE.
		if err := e.cfg.Unit.Kill(c); err != nil {
			return fmt.Errorf("force kill failed: %w", err)
		}
		kc, cancel2 := context.WithTimeout(c, e.cfg.KillGrace)
		defer cancel2()
		if err := e.cfg.Unit.WaitStopped(kc, time.Second); err != nil {
			return fmt.Errorf("process survived SIGKILL: %w", err)
		}
		return nil
	}); err != nil {
		// Nothing on disk was touched; server is running or hung, not
		// half-modified. Aborted is the honest status.
		return abort(StepStop, err)
	}

	// From here on, the server is DOWN. Every failure path must end with
	// an attempted restart of a known-good state (the pivot rule).

	// ---- BACKUP ----
	if err := e.step(ctx, id, p, StepBackup, p.Backup); err != nil {
		return e.recoverStart(ctx, id, p, StepBackup,
			"backup failed (partials cleaned by payload); restarting unchanged world: "+err.Error())
	}

	// ---- APPLY ----
	if err := e.step(ctx, id, p, StepApply, p.Apply); err != nil {
		if rerr := e.rollback(ctx, id, p, StepApply); rerr != nil {
			return e.doubleFault(id, p, StepApply, rerr)
		}
		return e.recoverStart(ctx, id, p, StepApply,
			"apply failed; pre-apply state restored; restarting: "+err.Error())
	}

	// ---- START: exactly one automatic rollback, never loops ----
	if err := e.step(ctx, id, p, StepStart, e.startAndWait); err != nil {
		if rerr := e.rollback(ctx, id, p, StepStart); rerr != nil {
			return e.doubleFault(id, p, StepStart, rerr)
		}
		if err2 := e.step(ctx, id, p, StepStart, e.startAndWait); err2 != nil {
			return e.haltedDown(id, p, StepStart,
				fmt.Sprintf("start failed (%v), rolled back, second start also failed (%v)", err, err2))
		}
		return Outcome{Status: StatusFailedRecovered, FailedStep: StepStart,
			Detail:  "start failed with new state; rolled back; server RUNNING on previous state: " + err.Error(),
			Anchors: p.Anchors()}
	}

	// ---- VERIFY: report honestly, never auto-rollback ----
	res, verr := p.Verify(ctx)
	if verr != nil {
		// A verify that couldn't run is a warning (we can't confirm), not
		// a mere note.
		res.Warnings = append(res.Warnings, "verify itself errored: "+verr.Error())
	}
	if len(res.Warnings) > 0 {
		e.cfg.Journal.StepFailed(id, string(StepVerify), fmt.Sprintf("%d warning(s)", len(res.Warnings)))
		e.emit(id, p, EventStepFailed, StepVerify, fmt.Sprintf("%d warning(s) — reported, not rolled back", len(res.Warnings)))
		return Outcome{Status: StatusSuccessWithWarnings, FailedStep: StepVerify,
			Detail: "applied and running; verify reported warnings", VerifyIssues: res.Warnings,
			VerifyNotes: res.Notes, Anchors: p.Anchors()}
	}
	// Only informational notes (or nothing) → clean success.
	e.cfg.Journal.StepOK(id, string(StepVerify))
	e.emit(id, p, EventStepOK, StepVerify, "")
	return Outcome{Status: StatusSuccess, VerifyNotes: res.Notes,
		Detail: "cycle completed; all changes verified"}
}

func (e *Engine) startAndWait(ctx context.Context) error {
	if err := e.cfg.Unit.Start(ctx); err != nil {
		return fmt.Errorf("unit start: %w", err)
	}
	sc, cancel := context.WithTimeout(ctx, e.cfg.StartTimeout)
	defer cancel()
	if err := e.cfg.API.WaitReady(sc, 2*time.Second); err != nil {
		return fmt.Errorf("server never reached genuine readiness: %w", err)
	}
	return nil
}

func (e *Engine) rollback(ctx context.Context, id string, p Payload, at Step) error {
	e.emit(id, p, EventRolledBack, at, "restoring pre-apply state")
	if err := p.RollbackApply(ctx); err != nil {
		return fmt.Errorf("rollback after %s failed: %w", at, err)
	}
	return nil
}

// recoverStart implements the pivot rule: after a post-STOP failure with
// the world unchanged (or restored), start the server again. If even that
// fails, halt.
func (e *Engine) recoverStart(ctx context.Context, id string, p Payload, failed Step, detail string) Outcome {
	if err := e.step(ctx, id, p, StepStart, e.startAndWait); err != nil {
		return e.haltedDown(id, p, failed, detail+" — AND recovery start failed: "+err.Error())
	}
	return Outcome{Status: StatusFailedRecovered, FailedStep: failed,
		Detail: detail + " — server RUNNING on previous state", Anchors: p.Anchors()}
}

func (e *Engine) doubleFault(id string, p Payload, at Step, rerr error) Outcome {
	e.emit(id, p, EventDoubleFault, at, rerr.Error())
	return e.haltedDown(id, p, at, "DOUBLE FAULT during rollback: "+rerr.Error())
}

// haltedDown: automation stops, anchors are named (I7), operator required.
func (e *Engine) haltedDown(id string, p Payload, at Step, detail string) Outcome {
	anchors := p.Anchors()
	e.emit(id, p, EventStepFailed, at, "HALTED, server DOWN. Recovery anchors: "+fmt.Sprint(anchors))
	return Outcome{Status: StatusHaltedDown, FailedStep: at, Detail: detail, Anchors: anchors}
}
