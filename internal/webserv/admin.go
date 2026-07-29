package webserv

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/juicetheforce/palworld-paladin/internal/backup"
)

// Lifecycle is the slice of the supervisor the Server Admin page needs
// (start/stop/restart via the scoped grant). *supervise.UnitController
// satisfies it.
type Lifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Restart(ctx context.Context) error
}

// Broadcaster sends a server-wide message. *palapi.Client satisfies it.
type Broadcaster interface {
	Announce(ctx context.Context, message string) error
	Save(ctx context.Context) error
}

// BackupManager is the slice of the backup manager the page needs.
type BackupManager interface {
	List() ([]backup.Entry, []string, error)
	Delete(id string) error
}

// ---- transaction history (in-memory, session-scoped) ----

// ActionKind labels a logged admin action.
type ActionKind string

// LogEntry is one recorded admin action for the transaction history.
type LogEntry struct {
	Time   time.Time `json:"time"`
	Action string    `json:"action"`
	Detail string    `json:"detail"`
	OK     bool      `json:"ok"`
}

// actionLog is a bounded in-memory ring of recent admin actions. Not
// persisted — it's a "what happened this session" view (matches the
// client-buffered philosophy elsewhere). Survives as long as the serve
// process runs.
type actionLog struct {
	mu      sync.Mutex
	entries []LogEntry
	max     int
}

func newActionLog(max int) *actionLog {
	if max <= 0 {
		max = 100
	}
	return &actionLog{max: max}
}

func (l *actionLog) add(action, detail string, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, LogEntry{Time: time.Now(), Action: action, Detail: detail, OK: ok})
	if len(l.entries) > l.max {
		l.entries = l.entries[len(l.entries)-l.max:]
	}
}

func (l *actionLog) list() []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LogEntry, len(l.entries))
	copy(out, l.entries)
	// newest first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// ---- handlers ----

type lifecycleReq struct {
	// Optional pre-action broadcast + delay (seconds). When set, the
	// server announces the message, waits Delay, then performs the action
	// — the same "warn the players" courtesy the maintenance engine's
	// ANNOUNCE step provides, exposed as a simple option here.
	Broadcast string `json:"broadcast"`
	Delay     int    `json:"delay_seconds"`
}

// handleLifecycle performs start/stop/restart (path action), optionally
// preceded by a broadcast + delay. Runs the delay in the request
// goroutine — the caller's HTTP request blocks until the action is issued,
// so the UI shows honest completion. (Long delays are the operator's
// choice; the client sets a generous timeout.)
func (s *Server) handleLifecycle(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	var req lifecycleReq
	json.NewDecoder(r.Body).Decode(&req) // body optional

	if s.lifecycle == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "lifecycle control not available"})
		return
	}

	// Optional warn-then-wait.
	if req.Broadcast != "" && s.broadcaster != nil {
		bctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		s.broadcaster.Announce(bctx, req.Broadcast)
		cancel()
		s.logAction("broadcast", req.Broadcast, true)
	}
	if req.Delay > 0 {
		select {
		case <-r.Context().Done():
			writeJSON(w, 499, map[string]string{"error": "cancelled"})
			return
		case <-time.After(time.Duration(req.Delay) * time.Second):
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	var err error
	switch action {
	case "start":
		err = s.lifecycle.Start(ctx)
	case "stop":
		err = s.lifecycle.Stop(ctx)
	case "restart":
		err = s.lifecycle.Restart(ctx)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown action"})
		return
	}
	s.logAction("server "+action, req.Broadcast, err == nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type broadcastReq struct {
	Message string `json:"message"`
}

func (s *Server) handleBroadcast(w http.ResponseWriter, r *http.Request) {
	var req broadcastReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Message == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message required"})
		return
	}
	if s.broadcaster == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "broadcast not available"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	err := s.broadcaster.Announce(ctx, req.Message)
	s.logAction("broadcast", req.Message, err == nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	if s.broadcaster == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "save not available"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	err := s.broadcaster.Save(ctx)
	s.logAction("save", "manual world save", err == nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// backupInfo is the catalog view for the admin page.
type backupInfo struct {
	ID        string    `json:"id"`
	Trigger   string    `json:"trigger"`
	Created   time.Time `json:"created"`
	SizeBytes int64     `json:"size_bytes"`
}

func (s *Server) handleBackupList(w http.ResponseWriter, r *http.Request) {
	if s.backupMgr == nil {
		writeJSON(w, http.StatusOK, map[string]any{"backups": []any{}})
		return
	}
	entries, partials, err := s.backupMgr.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := []backupInfo{}
	for _, e := range entries {
		out = append(out, backupInfo{ID: e.ID, Trigger: string(e.Trigger), Created: e.Created, SizeBytes: e.TotalSize})
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": out, "partials": len(partials)})
}

func (s *Server) handleBackupDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.backupMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backups not available"})
		return
	}
	if err := s.backupMgr.Delete(id); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.logAction("backup delete", id, true)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if s.actions == nil {
		writeJSON(w, http.StatusOK, map[string]any{"history": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"history": s.actions.list()})
}

func (s *Server) logAction(action, detail string, ok bool) {
	if s.actions != nil {
		s.actions.add(action, detail, ok)
	}
}
