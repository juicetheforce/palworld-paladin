package supervise

import (
	"path/filepath"
	"testing"
)

func TestRestartConfigValidation(t *testing.T) {
	ok := RestartConfig{Enabled: true, ThresholdGB: 12, Broadcast: "restarting", DelaySeconds: 60}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []RestartConfig{
		{Enabled: true, ThresholdGB: 0.5}, // below the idle-usage floor
		{Enabled: true, ThresholdGB: 9999},
		{Enabled: true, ThresholdGB: 12, DelaySeconds: -1},
		{Enabled: true, ThresholdGB: 12, DelaySeconds: 601},
	}
	for i, c := range bad {
		if err := c.Validate(); err == nil {
			t.Fatalf("case %d must fail validation: %+v", i, c)
		}
	}
	// Disabled configs skip validation (draft values are fine).
	if err := (RestartConfig{Enabled: false, ThresholdGB: 0.1}).Validate(); err != nil {
		t.Fatal("disabled config must not validate thresholds")
	}
}

func TestThresholdBytes(t *testing.T) {
	c := RestartConfig{Enabled: true, ThresholdGB: 2}
	if c.ThresholdBytes() != 2*(1<<30) {
		t.Fatalf("2 GB = %d bytes, got %d", 2*(1<<30), c.ThresholdBytes())
	}
	if (RestartConfig{Enabled: false, ThresholdGB: 2}).ThresholdBytes() != 0 {
		t.Fatal("disabled must yield 0 (supervisor-off)")
	}
}

func TestStorePersistAndApply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memrestart.json")
	var applied []RestartConfig
	st, err := LoadRestartConfigStore(path, func(c RestartConfig) { applied = append(applied, c) })
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || applied[0].Enabled {
		t.Fatalf("load must apply the (disabled) default once: %+v", applied)
	}

	set := RestartConfig{Enabled: true, ThresholdGB: 10, Broadcast: "mem restart", DelaySeconds: 30}
	if err := st.Set(set); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 2 || applied[1].ThresholdGB != 10 {
		t.Fatalf("Set must apply: %+v", applied)
	}

	// A fresh store from the same file loads the persisted config.
	st2, err := LoadRestartConfigStore(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := st2.Get(); !got.Enabled || got.ThresholdGB != 10 || got.Broadcast != "mem restart" {
		t.Fatalf("persistence failed: %+v", got)
	}

	// Invalid Set is rejected and does not clobber the stored config.
	if err := st.Set(RestartConfig{Enabled: true, ThresholdGB: 0.2}); err == nil {
		t.Fatal("invalid config must be rejected")
	}
	if st.Get().ThresholdGB != 10 {
		t.Fatal("rejected Set must not change the stored config")
	}
}
