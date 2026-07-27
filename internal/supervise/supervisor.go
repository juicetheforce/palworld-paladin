package supervise

import (
	"context"
	"sync"
	"time"
)

// EventKind labels supervisor observations for the audit trail (§6.9 I8).
type EventKind string

const (
	// EventRAMThresholdExceeded: unit memory crossed the configured
	// threshold and the restart action was invoked.
	EventRAMThresholdExceeded EventKind = "ram_threshold_exceeded"
	// EventRAMRearmed: memory fell back below threshold after a firing;
	// the trigger is armed again.
	EventRAMRearmed EventKind = "ram_rearmed"
	// EventUnitDown: the unit was observed not-active while supervision
	// was enabled. Recorded for the audit trail; the restart itself is
	// systemd's job (Restart=on-failure in the unit), which deliberately
	// does not fire on clean `systemctl stop` — so maintenance cycles
	// don't fight it (§6.9 I2 discussion).
	EventUnitDown EventKind = "unit_down_observed"
	// EventCheckError: a supervision poll failed (systemctl error etc.).
	EventCheckError EventKind = "check_error"
)

// Event is one supervisor observation.
type Event struct {
	Time   time.Time
	Kind   EventKind
	Detail string
	Memory uint64 // MemoryCurrent at observation time, when relevant
}

// Config configures a Supervisor.
type Config struct {
	// MemThresholdBytes: fire the restart action when the unit cgroup's
	// MemoryCurrent exceeds this. 0 disables the RAM watcher.
	MemThresholdBytes uint64
	// CheckInterval between polls. Default 15s.
	CheckInterval time.Duration
	// OnEvent receives every observation (for the store/audit log).
	// Called from the supervision goroutine; must not block long.
	OnEvent func(Event)
	// RestartAction is invoked (once per arming) when the RAM threshold
	// trips. Injected deliberately: a graceful RAM restart
	// (announce → save → restart) is a small maintenance cycle and
	// belongs to the maintain package; the supervisor only detects.
	// If nil, the event is emitted and nothing else happens.
	RestartAction func(ctx context.Context)
}

// Supervisor watches one unit. It implements invariant I2 (§6.9): the
// maintenance state machine calls Suspend() before a cycle and Resume()
// after (success or failure), and a suspended supervisor observes nothing
// and triggers nothing — structurally, not by convention.
type Supervisor struct {
	unit *UnitController
	cfg  Config

	mu        sync.Mutex
	suspended int  // suspension is counted: nested Suspend/Resume pairs are safe
	armed     bool // RAM trigger armed (re-arms only after dropping below threshold)
}

// NewSupervisor builds a Supervisor around a UnitController.
func NewSupervisor(unit *UnitController, cfg Config) *Supervisor {
	if cfg.CheckInterval <= 0 {
		cfg.CheckInterval = 15 * time.Second
	}
	return &Supervisor{unit: unit, cfg: cfg, armed: true}
}

// Suspend disables all supervision effects until a matching Resume.
// Counted, so nested maintenance operations compose safely.
func (s *Supervisor) Suspend() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.suspended++
}

// Resume re-enables supervision after Suspend. Extra Resumes are ignored
// rather than panicking: a recovery path that resumes "too many" times
// must not crash the tool (§6.9 double-fault posture).
func (s *Supervisor) Resume() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.suspended > 0 {
		s.suspended--
	}
}

// Suspended reports whether supervision is currently suspended.
func (s *Supervisor) Suspended() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.suspended > 0
}

func (s *Supervisor) emit(e Event) {
	if s.cfg.OnEvent != nil {
		e.Time = time.Now()
		s.cfg.OnEvent(e)
	}
}

// Run polls until ctx is done. Call in its own goroutine.
func (s *Supervisor) Run(ctx context.Context) {
	t := time.NewTicker(s.cfg.CheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.check(ctx)
		}
	}
}

// check performs one supervision poll. Exported logic kept in one place
// so tests can drive it tick-by-tick without real time.
func (s *Supervisor) check(ctx context.Context) {
	if s.Suspended() {
		return
	}
	p, err := s.unit.Show(ctx)
	if err != nil {
		s.emit(Event{Kind: EventCheckError, Detail: err.Error()})
		return
	}
	if p.ActiveState != "active" {
		s.emit(Event{Kind: EventUnitDown,
			Detail: "ActiveState=" + p.ActiveState + " SubState=" + p.SubState})
		return
	}
	if s.cfg.MemThresholdBytes == 0 {
		return
	}
	s.mu.Lock()
	armed := s.armed
	over := p.MemoryCurrent > s.cfg.MemThresholdBytes
	switch {
	case over && armed:
		s.armed = false // latch: one firing per excursion above threshold
	case !over && !armed:
		s.armed = true
	}
	s.mu.Unlock()

	if over && armed {
		s.emit(Event{Kind: EventRAMThresholdExceeded, Memory: p.MemoryCurrent,
			Detail: "restart action invoked"})
		if s.cfg.RestartAction != nil {
			s.cfg.RestartAction(ctx)
		}
	} else if !over && !armed {
		s.emit(Event{Kind: EventRAMRearmed, Memory: p.MemoryCurrent})
	}
}
