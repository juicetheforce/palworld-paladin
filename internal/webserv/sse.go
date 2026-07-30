package webserv

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// handleEvents is the SSE endpoint. The browser opens a persistent
// connection here and receives a stream of JSON events (log lines,
// lifecycle progress, operation results). This is the shared live channel
// for the Server Admin log viewer and lifecycle progress — and, later,
// commit/restore/update progress.
//
// SSE (text/event-stream) is chosen over WebSockets deliberately: it's
// one-directional (server→browser, which is all we need), rides plain
// HTTP (no upgrade dance, works through the same auth), and the browser's
// EventSource auto-reconnects. Simpler and sufficient.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "events not available"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering if present

	ch, unsub := s.hub.Subscribe()
	defer unsub()

	// Greet so the client knows the stream is live.
	fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	// Heartbeat keeps intermediaries from timing out an idle stream and
	// lets us notice a dead client (write error → return → unsub).
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := fmt.Fprintf(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case e, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(e)
			if err != nil {
				continue
			}
			// SSE frame: "event: <kind>" + "data: <json>".
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Kind, data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// handleEventsRecent is tail-on-connect: the recent-history payload a page
// fetches on load so the live viewer starts populated instead of blank.
// Two halves, two sources of truth: Paladin's own recent events from the
// hub's ring buffer (they exist nowhere else), and the last lines of
// Pal.log read straight from disk (the file is its own history). A server
// launched without -log yields an empty log_tail, never an error.
func (s *Server) handleEventsRecent(w http.ResponseWriter, r *http.Request) {
	if s.hub == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "events not available"})
		return
	}
	resp := map[string]any{"events": s.hub.Recent()}
	if s.logTail != nil {
		if lines, err := s.logTail(20); err == nil {
			resp["log_tail"] = lines
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
