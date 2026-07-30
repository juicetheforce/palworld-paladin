package events

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestFanOut(t *testing.T) {
	h := NewHub(16)
	chA, unsubA := h.Subscribe()
	chB, unsubB := h.Subscribe()
	defer unsubA()
	defer unsubB()

	h.Log("hello")
	for _, ch := range []<-chan Event{chA, chB} {
		select {
		case e := <-ch:
			if e.Kind != KindLog || e.Msg != "hello" {
				t.Fatalf("bad event: %+v", e)
			}
			if e.Time.IsZero() {
				t.Fatal("Publish should stamp Time")
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive event")
		}
	}
}

func TestUnsubscribeStopsDelivery(t *testing.T) {
	h := NewHub(16)
	ch, unsub := h.Subscribe()
	unsub()
	// Channel is closed; a receive should not block and not get a value.
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected closed channel after unsubscribe")
		}
	case <-time.After(time.Second):
		t.Fatal("closed channel should be immediately receivable")
	}
	if h.SubscriberCount() != 0 {
		t.Fatalf("want 0 subscribers, got %d", h.SubscriberCount())
	}
}

func TestSlowSubscriberDropsRatherThanBlocks(t *testing.T) {
	h := NewHub(2) // tiny buffer
	_, unsub := h.Subscribe()
	defer unsub()

	// Publish many more than the buffer holds. If Publish blocked on the
	// full subscriber, this would deadlock; it must return promptly.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			h.Log("flood")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber (should drop instead)")
	}
}

func TestConcurrentPublishSubscribe(t *testing.T) {
	h := NewHub(64)
	var wg sync.WaitGroup
	// Churn subscribers while publishing — race detector should stay quiet.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, unsub := h.Subscribe()
			go func() {
				for range ch {
				}
			}()
			time.Sleep(time.Millisecond)
			unsub()
		}()
	}
	for i := 0; i < 100; i++ {
		h.Log("x")
	}
	wg.Wait()
}

func TestTerminalEventsCarryOK(t *testing.T) {
	h := NewHub(16)
	ch, unsub := h.Subscribe()
	defer unsub()
	h.Done("restart", "done", true)
	e := <-ch
	if e.Kind != KindDone || e.OK == nil || !*e.OK {
		t.Fatalf("Done should carry OK=true: %+v", e)
	}
}

// ---- event log persistence ----

func TestEventLogAppendLoadAndRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l, err := OpenEventLog(path, 600) // tiny cap to force rotation
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 12; i++ {
		if err := l.Append(Event{Seq: uint64(i), Kind: KindPlayer, Msg: fmt.Sprintf("player-%02d joined", i)}); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()

	if _, err := os.Stat(path + ".old"); err != nil {
		t.Fatal("rotation must have produced a .old generation")
	}

	// LoadRecent spans the rotation boundary, oldest first.
	evs, err := LoadRecent(path, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 8 {
		t.Fatalf("want 8 events across generations, got %d", len(evs))
	}
	if evs[len(evs)-1].Msg != "player-12 joined" {
		t.Fatalf("last event must be the newest: %s", evs[len(evs)-1].Msg)
	}
	for i := 1; i < len(evs); i++ {
		if evs[i].Seq <= evs[i-1].Seq {
			t.Fatal("events must be oldest-first")
		}
	}
}

func TestHubPersistAndSeedContinuity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l, _ := OpenEventLog(path, 1<<20)

	// First "run": events flow through a hub with persistence.
	h1 := NewHub(16)
	h1.SetPersist(func(e Event) { l.Append(e) })
	h1.Player("alice joined the server")
	h1.Done("update", "Update complete", true)
	h1.Log("a log line — must NOT persist")
	l.Close()

	// Second "run": a fresh hub seeds from the file.
	loaded, _ := LoadRecent(path, 100)
	if len(loaded) != 2 {
		t.Fatalf("only ring-worthy events persist: got %d", len(loaded))
	}
	h2 := NewHub(16)
	h2.Seed(loaded)
	rec := h2.Recent()
	if len(rec) != 2 || rec[0].Msg != "alice joined the server" {
		t.Fatalf("seeded history must appear in Recent(): %+v", rec)
	}
	// New events must continue past the seeded seq, never collide.
	h2.Player("bob joined the server")
	rec = h2.Recent()
	last := rec[len(rec)-1]
	if last.Seq <= loaded[len(loaded)-1].Seq {
		t.Fatalf("live seq must continue past seeded seq: %d vs %d", last.Seq, loaded[len(loaded)-1].Seq)
	}
}
