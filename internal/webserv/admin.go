package webserv

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/juicetheforce/palworld-paladin/internal/backup"
	"github.com/juicetheforce/palworld-paladin/internal/supervise"
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

// Readiness reports when the server is actually up (REST responding), so
// start/restart can honestly confirm "started successfully" rather than
// just "command issued." *palapi.Client satisfies it via WaitReady.
type Readiness interface {
	WaitReady(ctx context.Context, interval time.Duration) error
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
// preceded by a broadcast + delay. It runs the operation in a background
// goroutine and streams live progress over SSE (the event hub), returning
// 202 immediately so the UI can watch the operation unfold — including the
// honest "started successfully" confirmation once REST actually responds.
func (s *Server) handleLifecycle(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	var req lifecycleReq
	json.NewDecoder(r.Body).Decode(&req) // body optional

	if s.lifecycle == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "lifecycle control not available"})
		return
	}
	if action != "start" && action != "stop" && action != "restart" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown action"})
		return
	}

	// Run the operation detached from the request so a long warn-delay or
	// boot wait doesn't hinge on the HTTP connection staying open. Progress
	// streams over SSE; the request returns 202 Accepted right away.
	go s.runLifecycle(action, req)

	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

// runLifecycle executes a lifecycle operation, publishing progress to the
// event hub at each step. Uses a background context (not the request's) so
// it completes even after the HTTP response is sent.
func (s *Server) runLifecycle(action string, req lifecycleReq) {
	pub := func(step, msg string) {
		if s.hub != nil {
			s.hub.Progress(action, step, msg)
		}
	}

	// Optional warn-then-wait.
	if req.Broadcast != "" && s.broadcaster != nil {
		bctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		s.broadcaster.Announce(bctx, req.Broadcast)
		cancel()
		s.logAction("broadcast", req.Broadcast, true)
		pub("announce", "Warned players: "+req.Broadcast)
	}
	if req.Delay > 0 {
		pub("delay", fmt.Sprintf("Waiting %ds before %s…", req.Delay, action))
		time.Sleep(time.Duration(req.Delay) * time.Second)
	}

	pub(action, actionGerund(action)+"…")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var err error
	switch action {
	case "start":
		err = s.lifecycle.Start(ctx)
	case "stop":
		err = s.lifecycle.Stop(ctx)
	case "restart":
		err = s.lifecycle.Restart(ctx)
	}
	if err != nil {
		s.logAction("server "+action, req.Broadcast, false)
		if s.hub != nil {
			s.hub.Error(action, "Failed to "+action+": "+err.Error())
		}
		return
	}

	// For start/restart, close the loop honestly: wait for REST to respond
	// before declaring success. Stop needs no readiness wait.
	if (action == "start" || action == "restart") && s.readiness != nil {
		pub(action, "Waiting for server to respond…")
		rctx, rcancel := context.WithTimeout(context.Background(), 90*time.Second)
		rerr := s.readiness.WaitReady(rctx, 2*time.Second)
		rcancel()
		if rerr != nil {
			s.logAction("server "+action, "did not confirm ready", false)
			if s.hub != nil {
				s.hub.Error(action, "Server "+action+" issued but it did not come up in time: "+rerr.Error())
			}
			return
		}
	}

	msg := successMessage(action)
	s.logAction("server "+action, msg, true)
	if s.hub != nil {
		s.hub.Done(action, msg, true)
	}
}

func actionGerund(a string) string {
	switch a {
	case "start":
		return "Starting server"
	case "stop":
		return "Stopping server"
	case "restart":
		return "Restarting server"
	}
	return a
}

func successMessage(a string) string {
	switch a {
	case "start":
		return "Server started successfully"
	case "restart":
		return "Restart successful — server is responding"
	case "stop":
		return "Server stopped"
	}
	return a + " complete"
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
	s.recordAction("broadcast", req.Message, err == nil)
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
	s.recordAction("save", "world flushed to disk", err == nil)
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

// recordAction logs to the history card AND publishes a live-stream event,
// so every admin action shows up consistently in both the Recent actions
// list and the Live activity feed. One-shot actions (broadcast, save) use
// this; multi-step operations (lifecycle) stream their own progress and
// call logAction directly for the terminal record.
func (s *Server) recordAction(action, detail string, ok bool) {
	s.logAction(action, detail, ok)
	if s.hub != nil {
		msg := action
		if detail != "" {
			msg = action + ": " + detail
		}
		s.hub.Done(action, msg, ok)
	}
}

// ---- server update ----

// UpdateResult is what the injected update runner reports back.
type UpdateResult struct {
	Status   string // maintain.Status string: success / success_with_warnings / aborted / ...
	Detail   string
	UpToDate bool // clean no-op: no update existed, server untouched
}

// UpdateRunner runs one full update cycle (announce → save → stop → backup
// → steamcmd → start → verify). Wired in main.go where the engine and
// steam deps live; progress streams over the event hub independently.
type UpdateRunner func(ctx context.Context, broadcast string, delaySec int) UpdateResult

type updateReq struct {
	Broadcast string `json:"broadcast"`
	Delay     int    `json:"delay_seconds"`
}

// handleUpdate kicks the update cycle in the background and returns 202.
// A handler-level busy guard gives a clean 409 on double-click; the
// engine's single-flight lock (I1) remains the real protection underneath.
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if s.update == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "update not available"})
		return
	}
	var req updateReq
	json.NewDecoder(r.Body).Decode(&req) // body optional

	if !s.updateBusy.CompareAndSwap(false, true) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "an update is already running"})
		return
	}

	go func() {
		defer s.updateBusy.Store(false)
		res := s.update(context.Background(), req.Broadcast, req.Delay)
		ok := res.UpToDate || res.Status == "success" || res.Status == "success_with_warnings"
		detail := res.Detail
		if res.UpToDate {
			detail = "already up to date"
		}
		s.logAction("update", detail, ok)
	}()

	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

// ---- memory-threshold auto-restart config ----

// MemRestartStore is the slice of the config store the page needs.
// *supervise.RestartConfigStore satisfies it.
type MemRestartStore interface {
	Get() supervise.RestartConfig
	Set(supervise.RestartConfig) error
}

// UnitMemoryFunc reports the game unit's current memory use (cgroup
// MemoryCurrent), shown beside the threshold field so the operator sets it
// against reality rather than guessing.
type UnitMemoryFunc func(ctx context.Context) (uint64, error)

func (s *Server) handleMemRestartGet(w http.ResponseWriter, r *http.Request) {
	if s.memRestart == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	resp := map[string]any{"available": true, "config": s.memRestart.Get()}
	if s.unitMemory != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		if mem, err := s.unitMemory(ctx); err == nil {
			resp["current_memory_bytes"] = mem
		}
		cancel()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleMemRestartSet(w http.ResponseWriter, r *http.Request) {
	if s.memRestart == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "not available"})
		return
	}
	var cfg supervise.RestartConfig
	if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if err := s.memRestart.Set(cfg); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	detail := "disabled"
	if cfg.Enabled {
		detail = fmt.Sprintf("threshold %.1f GB", cfg.ThresholdGB)
		if cfg.Broadcast != "" {
			detail += fmt.Sprintf(", warn + %ds delay", cfg.DelaySeconds)
		} else {
			detail += ", immediate"
		}
	}
	s.recordAction("memory auto-restart", detail, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": s.memRestart.Get()})
}

// RecordAction is the exported history+live-stream hook for actions that
// originate outside the HTTP layer (e.g. the memory-threshold restart,
// which the supervisor fires on its own). Same consistency guarantee as
// handler-driven actions: it lands in Recent actions AND Live activity.
func (s *Server) RecordAction(action, detail string, ok bool) {
	s.recordAction(action, detail, ok)
}
