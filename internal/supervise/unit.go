package supervise

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Runner executes a command and returns its combined output. It is the
// seam that (a) makes this package fully testable with a fake, and (b)
// keeps the scoped-grant mechanism (sudoers vs polkit, DESIGN.md §11)
// swappable: elevation changes how commands run, not this package.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (output string, err error)
}

// ExecRunner runs commands directly via os/exec.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(out), err
}

// UnitController owns exactly one systemd unit (the game server's,
// authored by Paladin — DESIGN.md §6.1) and controls it via systemctl.
type UnitController struct {
	Unit   string
	Runner Runner
}

// NewUnitController returns a controller for the named unit using the
// real ExecRunner.
func NewUnitController(unit string) *UnitController {
	return &UnitController{Unit: unit, Runner: ExecRunner{}}
}

func (u *UnitController) systemctl(ctx context.Context, args ...string) (string, error) {
	out, err := u.Runner.Run(ctx, "systemctl", args...)
	if err != nil {
		return out, fmt.Errorf("systemctl %s: %w (output: %s)",
			strings.Join(args, " "), err, strings.TrimSpace(out))
	}
	return out, nil
}

// Start starts the unit. Readiness is a separate concern: callers pair
// this with palapi's WaitReady, because "unit active" is not "server
// serving" (DESIGN.md §6.1).
func (u *UnitController) Start(ctx context.Context) error {
	_, err := u.systemctl(ctx, "start", u.Unit)
	return err
}

// Stop requests a graceful stop. It does NOT confirm the process is gone;
// use WaitStopped for that (the maintenance STOP step requires
// confirmation, §6.3 step 4).
func (u *UnitController) Stop(ctx context.Context) error {
	_, err := u.systemctl(ctx, "stop", u.Unit)
	return err
}

// Restart restarts the unit (systemd stop+start as one operation).
func (u *UnitController) Restart(ctx context.Context) error {
	_, err := u.systemctl(ctx, "restart", u.Unit)
	return err
}

// Kill sends SIGKILL to all unit processes. ONLY for the user-initiated
// force-kill path of the STOP escalation dialog (§6.9) — never called
// automatically by anything in this codebase.
func (u *UnitController) Kill(ctx context.Context) error {
	_, err := u.systemctl(ctx, "kill", "-s", "SIGKILL", u.Unit)
	return err
}

// Props is the parsed subset of `systemctl show` this package relies on.
type Props struct {
	ActiveState   string // active, inactive, failed, activating, deactivating
	SubState      string
	MainPID       int
	MemoryCurrent uint64 // bytes, whole unit cgroup (shell + game + crashpad)
	// MemoryCurrent deliberately comes from the unit cgroup, not MainPID:
	// MainPID is PalServer.sh, a tiny shell; the memory lives in its
	// child PalServer-Linux-Shipping. The cgroup number is the one that
	// matches `systemctl status`'s "Memory:" line and the one the
	// RAM-threshold restart must watch.
}

// Show reads the unit's current properties.
func (u *UnitController) Show(ctx context.Context) (*Props, error) {
	out, err := u.systemctl(ctx, "show", u.Unit,
		"--property=ActiveState,SubState,MainPID,MemoryCurrent")
	if err != nil {
		return nil, err
	}
	p := &Props{}
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch k {
		case "ActiveState":
			p.ActiveState = v
		case "SubState":
			p.SubState = v
		case "MainPID":
			p.MainPID, _ = strconv.Atoi(v)
		case "MemoryCurrent":
			// systemd reports "[not set]" when the unit is down or
			// accounting is unavailable; treat as 0.
			if n, err := strconv.ParseUint(v, 10, 64); err == nil {
				p.MemoryCurrent = n
			}
		}
	}
	if p.ActiveState == "" {
		return nil, fmt.Errorf("supervise: could not parse ActiveState from systemctl show output: %q", out)
	}
	return p, nil
}

// IsActive reports whether the unit is in ActiveState=active.
func (u *UnitController) IsActive(ctx context.Context) (bool, error) {
	p, err := u.Show(ctx)
	if err != nil {
		return false, err
	}
	return p.ActiveState == "active", nil
}

// WaitStopped polls until the unit is fully down (inactive/failed with
// MainPID 0) or ctx expires. This is the STOP step's "confirm the process
// is gone" (§6.3 step 4); on timeout the caller escalates to the §6.9
// user dialog — this function never kills anything itself.
func (u *UnitController) WaitStopped(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		p, err := u.Show(ctx)
		if err == nil &&
			(p.ActiveState == "inactive" || p.ActiveState == "failed") &&
			p.MainPID == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("supervise: unit %s did not stop: %w", u.Unit, ctx.Err())
		case <-t.C:
		}
	}
}
