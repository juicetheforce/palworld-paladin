// Package roster derives player join/leave events by diffing consecutive
// REST roster polls. This is the launch-flag-independent player-event
// source (rev 16): Pal.log only exists when the server was launched with
// -log, but the roster is always available, so the live viewer's player
// events never depend on how the server was started.
package roster

import (
	"context"
	"sync"

	"github.com/juicetheforce/palworld-paladin/internal/palapi"
)

// ListFunc returns the current roster (palapi.Client.Players).
type ListFunc func(ctx context.Context) ([]palapi.Player, error)

// Differ tracks roster changes between polls.
type Differ struct {
	list    ListFunc
	onJoin  func(p palapi.Player)
	onLeave func(p palapi.Player)

	mu       sync.Mutex
	known    map[string]palapi.Player // by UserID (the stable identifier)
	baseline bool                     // first successful poll seeds silently
}

// New creates a Differ. The first successful poll establishes a baseline
// without emitting events — otherwise every Paladin restart would announce
// all currently-connected players as freshly "joined".
func New(list ListFunc, onJoin, onLeave func(p palapi.Player)) *Differ {
	return &Differ{list: list, onJoin: onJoin, onLeave: onLeave, known: map[string]palapi.Player{}}
}

// Poll fetches the roster once and emits join/leave events for the
// difference against the previous successful poll. Poll errors (server
// down, restarting) leave the known set untouched: events are only
// derived between two SUCCESSFUL polls, so a transient REST hiccup never
// fabricates a wave of leaves and rejoins.
func (d *Differ) Poll(ctx context.Context) error {
	players, err := d.list(ctx)
	if err != nil {
		return err
	}
	now := make(map[string]palapi.Player, len(players))
	for _, p := range players {
		id := p.UserID
		if id == "" {
			id = p.PlayerID // fallback identifier
		}
		now[id] = p
	}

	d.mu.Lock()
	prev := d.known
	first := !d.baseline
	d.known = now
	d.baseline = true
	d.mu.Unlock()

	if first {
		return nil // baseline established silently
	}
	for id, p := range now {
		if _, was := prev[id]; !was && d.onJoin != nil {
			d.onJoin(p)
		}
	}
	for id, p := range prev {
		if _, still := now[id]; !still && d.onLeave != nil {
			d.onLeave(p)
		}
	}
	return nil
}
