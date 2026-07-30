// Package events is Paladin's live-event backbone: a small in-process
// pub/sub hub plus typed events, streamed to the browser over SSE
// (Server-Sent Events). It's the shared foundation for the live server-log
// viewer, live lifecycle progress, and (later) commit/restore/update
// progress — one streaming mechanism, many producers.
//
// Design: fan-out to N subscribers, each with a buffered channel. A slow
// subscriber that fills its buffer drops events (never blocks producers) —
// live progress is disposable; missing a line is better than stalling the
// server. Subscribers are the browser tabs watching the page.
package events

import (
	"sync"
	"time"
)

// Kind classifies an event so the frontend can route/style it.
type Kind string

const (
	KindLog       Kind = "log"       // a line from the server log
	KindProgress  Kind = "progress"  // a step in a multi-stage operation
	KindLifecycle Kind = "lifecycle" // start/stop/restart status change
	KindError     Kind = "error"     // an operation-level error
	KindDone      Kind = "done"      // an operation finished (ok or not)
	KindPlayer    Kind = "player"    // a player joined or left (roster differ)
)

// Event is one streamed item. Kept small and JSON-friendly.
type Event struct {
	Seq   uint64    `json:"seq"` // monotonic, for history/live dedup
	Kind  Kind      `json:"kind"`
	Time  time.Time `json:"time"`
	Op    string    `json:"op,omitempty"`    // which operation (e.g. "restart", "update")
	Msg   string    `json:"msg"`             // human-readable line
	Step  string    `json:"step,omitempty"`  // machine step id, for progress
	OK    *bool     `json:"ok,omitempty"`    // set on terminal events
	Extra string    `json:"extra,omitempty"` // optional detail
}

// Hub fans events out to all current subscribers.
type Hub struct {
	mu     sync.RWMutex
	subs   map[int]chan Event
	nextID int
	bufLen int
	seq    uint64
	recent []Event // ring of recent non-log events (history for page loads)
}

// recentCap: how many of Paladin's own events survive for tail-on-connect.
// Log lines are excluded — Pal.log on disk is already their history.
const recentCap = 100

// NewHub creates a hub. bufLen is the per-subscriber buffer (events beyond
// it are dropped for that subscriber rather than blocking the producer).
func NewHub(bufLen int) *Hub {
	if bufLen <= 0 {
		bufLen = 256
	}
	return &Hub{subs: map[int]chan Event{}, bufLen: bufLen}
}

// Subscribe registers a new subscriber, returning its channel and an
// unsubscribe func. The caller must call unsubscribe when done (e.g. when
// the SSE request ends) to avoid leaking the channel.
func (h *Hub) Subscribe() (<-chan Event, func()) {
	h.mu.Lock()
	id := h.nextID
	h.nextID++
	ch := make(chan Event, h.bufLen)
	h.subs[id] = ch
	h.mu.Unlock()

	return ch, func() {
		h.mu.Lock()
		if c, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(c)
		}
		h.mu.Unlock()
	}
}

// Publish delivers an event to all subscribers. Never blocks: a subscriber
// whose buffer is full misses this event. Stamps Time if unset.
// Recent returns the buffered recent events, oldest first.
func (h *Hub) Recent() []Event {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Event, len(h.recent))
	copy(out, h.recent)
	return out
}

func (h *Hub) Publish(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	h.mu.Lock()
	h.seq++
	e.Seq = h.seq
	if e.Kind != KindLog { // log lines: the file is their history
		h.recent = append(h.recent, e)
		if len(h.recent) > recentCap {
			h.recent = h.recent[len(h.recent)-recentCap:]
		}
	}
	h.mu.Unlock()
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, ch := range h.subs {
		select {
		case ch <- e:
		default:
			// Subscriber is behind; drop rather than stall the producer.
		}
	}
}

// SubscriberCount reports how many subscribers are active (used to avoid
// expensive producers — like log tailing — when nobody's watching).
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

// ---- convenience publishers ----

func boolp(b bool) *bool { return &b }

// Log publishes a server-log line.
func (h *Hub) Log(line string) { h.Publish(Event{Kind: KindLog, Msg: line}) }

// Progress publishes a step in a named operation.
func (h *Hub) Progress(op, step, msg string) {
	h.Publish(Event{Kind: KindProgress, Op: op, Step: step, Msg: msg})
}

// Lifecycle publishes a lifecycle status change.
func (h *Hub) Lifecycle(op, msg string) {
	h.Publish(Event{Kind: KindLifecycle, Op: op, Msg: msg})
}

// Done publishes a terminal event for an operation.
func (h *Hub) Done(op, msg string, ok bool) {
	h.Publish(Event{Kind: KindDone, Op: op, Msg: msg, OK: boolp(ok)})
}

// Errorf publishes an operation-level error.
// Player publishes a roster event (join/leave).
func (h *Hub) Player(msg string) { h.Publish(Event{Kind: KindPlayer, Op: "roster", Msg: msg}) }

func (h *Hub) Error(op, msg string) {
	h.Publish(Event{Kind: KindError, Op: op, Msg: msg, OK: boolp(false)})
}
