package hostmetrics

import (
	"testing"
)

// These run against the sandbox's real /proc and /sys — a live smoke of
// the readers. They assert structure and sanity, not exact values.

func TestReadProducesSaneSnapshot(t *testing.T) {
	r := NewReader()
	first := r.Read() // establishes baselines; rates read 0
	if first.MemTotal == 0 {
		t.Fatal("MemTotal should be readable on any Linux host")
	}
	if first.CPUCores == 0 || first.CPUModel == "" {
		t.Fatalf("CPU static info should be present: cores=%d model=%q", first.CPUCores, first.CPUModel)
	}
	// First read has no prior sample → CPU rate not yet available.
	if first.CPUAvailable {
		t.Log("note: CPUAvailable true on first read (unexpected but not fatal)")
	}
}

func TestCPUBusyPctBounds(t *testing.T) {
	// idle-only delta → 0% busy
	prev := cpuTimes{idle: 100}
	cur := cpuTimes{idle: 200}
	if p := cpuBusyPct(prev, cur); p != 0 {
		t.Fatalf("all-idle should be 0%%, got %v", p)
	}
	// all-busy delta → 100%
	prev = cpuTimes{user: 100}
	cur = cpuTimes{user: 200}
	if p := cpuBusyPct(prev, cur); p != 100 {
		t.Fatalf("all-user should be 100%%, got %v", p)
	}
	// mixed → 50%
	prev = cpuTimes{user: 100, idle: 100}
	cur = cpuTimes{user: 150, idle: 150}
	if p := cpuBusyPct(prev, cur); p != 50 {
		t.Fatalf("half-busy should be 50%%, got %v", p)
	}
	// no elapsed ticks → 0, no divide-by-zero
	if p := cpuBusyPct(cpuTimes{}, cpuTimes{}); p != 0 {
		t.Fatalf("zero delta should be 0%%, got %v", p)
	}
}

func TestMemUsedNeverUnderflows(t *testing.T) {
	s := &Snapshot{}
	readMemory(s)
	if s.MemUsed > s.MemTotal {
		t.Fatalf("MemUsed (%d) must not exceed MemTotal (%d)", s.MemUsed, s.MemTotal)
	}
}

func TestGracefulDegradation(t *testing.T) {
	// Temp and network availability are environment-dependent; the
	// contract is only that absence is signalled, never panics. Reading
	// twice exercises the rate path.
	r := NewReader()
	r.Read()
	s := r.Read()
	// If temps are unavailable (typical in CI/containers), the flag must
	// be false and the value zero — not a stale/garbage reading.
	if !s.TempAvailable && s.CPUTemp != 0 {
		t.Fatalf("temp unavailable but value non-zero: %v", s.CPUTemp)
	}
	if !s.NetAvailable && (s.NetRxBytesPerSec != 0 || s.NetTxBytesPerSec != 0) {
		t.Fatal("net unavailable but throughput non-zero")
	}
}
