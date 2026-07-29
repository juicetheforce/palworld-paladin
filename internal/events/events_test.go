package events

import (
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
