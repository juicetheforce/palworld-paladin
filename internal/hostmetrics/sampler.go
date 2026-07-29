package hostmetrics

import (
	"context"
	"sync"
	"time"
)

// Sampler runs a Reader on a timer so rate-based metrics (CPU %, network
// throughput) are always warm — the browser can poll /api/host at any
// cadence and get correct deltas, because the sampler maintains the
// between-sample state independently (DESIGN.md §6.6a). It holds only the
// single latest snapshot: no history ring buffer (history is client-side,
// the Proxmox/pfSense pattern).
type Sampler struct {
	reader   *Reader
	interval time.Duration
	mu       sync.RWMutex
	latest   Snapshot
}

// NewSampler creates a sampler with the given interval (default 3s).
func NewSampler(interval time.Duration) *Sampler {
	if interval <= 0 {
		interval = 3 * time.Second
	}
	s := &Sampler{reader: NewReader(), interval: interval}
	// Prime immediately so the first HTTP read isn't empty (rates 0 until
	// the second tick, which is correct).
	s.latest = s.reader.Read()
	return s
}

// Run samples until ctx is cancelled. Call in a goroutine.
func (s *Sampler) Run(ctx context.Context) {
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			snap := s.reader.Read()
			s.mu.Lock()
			s.latest = snap
			s.mu.Unlock()
		}
	}
}

// Latest returns the most recent snapshot (satisfies webserv.HostProvider).
func (s *Sampler) Latest() any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latest
}
