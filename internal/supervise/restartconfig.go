package supervise

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// RestartConfig is the operator-facing memory-threshold auto-restart
// setting (DESIGN.md §6.1, rev 16). The graceful-vs-immediate choice is
// inferred from the fields, not a separate mode: an empty Broadcast AND a
// zero Delay mean the restart fires immediately; setting them means
// warn-then-wait-then-restart. Save-before-restart always happens and is
// not configurable — no player should lose progress to a leak restart.
type RestartConfig struct {
	Enabled      bool    `json:"enabled"`
	ThresholdGB  float64 `json:"threshold_gb"`
	Broadcast    string  `json:"broadcast"`
	DelaySeconds int     `json:"delay_seconds"`
}

// ThresholdBytes converts the operator's GB figure to the supervisor's
// bytes (0 when disabled — which is how the supervisor understands "off").
func (c RestartConfig) ThresholdBytes() uint64 {
	if !c.Enabled || c.ThresholdGB <= 0 {
		return 0
	}
	return uint64(c.ThresholdGB * float64(1<<30))
}

// Validate enforces sane bounds. The floor is deliberately LOW (0.5 GB):
// it only rejects nonsense values (zero, typos). A threshold below the
// server's current usage is a legitimate operator choice — useful for
// testing the trip — and is handled by informed consent (the UI warns
// when threshold ≤ live usage) plus the supervisor's firing cooldown,
// not by a validator guessing what "too low" means for every server.
func (c RestartConfig) Validate() error {
	if !c.Enabled {
		return nil // disabled config stores whatever draft values it likes
	}
	if c.ThresholdGB < 0.5 || c.ThresholdGB > 512 {
		return fmt.Errorf("threshold must be between 0.5 and 512 GB")
	}
	if c.DelaySeconds < 0 || c.DelaySeconds > 600 {
		return fmt.Errorf("delay must be between 0 and 600 seconds")
	}
	if len(c.Broadcast) > 200 {
		return fmt.Errorf("broadcast message too long (max 200 characters)")
	}
	return nil
}

// RestartConfigStore persists a RestartConfig as JSON and notifies an
// apply callback on change (which pushes the threshold into the running
// supervisor). Safe for concurrent use.
type RestartConfigStore struct {
	path    string
	mu      sync.Mutex
	current RestartConfig
	onApply func(RestartConfig)
}

// LoadRestartConfigStore opens (or initializes) the store. A missing file
// is a disabled config, not an error. onApply is invoked once with the
// loaded config and again on every successful Set.
func LoadRestartConfigStore(path string, onApply func(RestartConfig)) (*RestartConfigStore, error) {
	s := &RestartConfigStore{path: path, onApply: onApply}
	b, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		// defaults: disabled
	case err != nil:
		return nil, fmt.Errorf("mem-restart config: read %s: %w", path, err)
	default:
		if err := json.Unmarshal(b, &s.current); err != nil {
			return nil, fmt.Errorf("mem-restart config: parse %s: %w", path, err)
		}
	}
	if s.onApply != nil {
		s.onApply(s.current)
	}
	return s, nil
}

// Get returns the current config.
func (s *RestartConfigStore) Get() RestartConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// Set validates, persists, and applies a new config.
func (s *RestartConfigStore) Set(c RestartConfig) error {
	if err := c.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(s.path, b, 0o600); err != nil {
		return err
	}
	s.current = c
	if s.onApply != nil {
		s.onApply(c)
	}
	return nil
}
