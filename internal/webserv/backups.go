package webserv

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/juicetheforce/palworld-paladin/internal/backup"
)

// CreateBackupFunc makes a manual backup (main.go wires save-then-copy so
// the backup captures the latest world state). Returns the new entry.
type CreateBackupFunc func(ctx context.Context) (*backup.Entry, error)

// RestoreResult mirrors UpdateResult for the restore cycle.
type RestoreResult struct {
	Status string
	Detail string
}

// RestoreRunner runs one full restore cycle for a backup ID (announce →
// save → stop → safety-copy → swap-in → start → verify). Progress streams
// over the hub; wired in main.go beside the update runner.
type RestoreRunner func(ctx context.Context, backupID, broadcast string, delaySec int) RestoreResult

// ResetResult mirrors RestoreResult for the full-server-reset cycle.
type ResetResult struct {
	Status string
	Detail string
}

// ResetRunner runs one full server-reset cycle (announce → save → stop →
// pre-reset backup → wipe → start → verify). The typed confirmation is
// enforced at the HTTP layer; by the time this runs, the operator has
// been asked properly.
type ResetRunner func(ctx context.Context, opts ResetOptions) ResetResult

// ResetOptions carries the operator's choices from the danger-zone form.
type ResetOptions struct {
	KeepSettings   bool   // leave PalWorldSettings.ini alone (default: reset)
	WipePlayerData bool   // clear known-players history and ban list
	Broadcast      string // warning message ("" = none)
	Delay          int    // seconds between warning and stop
}

// resetConfirmWord is the typed gate for the reset. Palworld flavour on
// purpose: this is a game server, not a nuclear silo — but you still have
// to spell it right.
const resetConfirmWord = "ASTRALYM"

var errBackupBusy = "a backup or restore is already running"

// handleBackupCreate kicks a manual backup in the background (worlds can
// be large; a copy can take a while) and returns 202. Terminal state
// arrives on the live stream and in the history.
func (s *Server) handleBackupCreate(w http.ResponseWriter, r *http.Request) {
	if s.createBackup == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backup create not available"})
		return
	}
	if !s.backupBusy.CompareAndSwap(false, true) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": errBackupBusy})
		return
	}
	go func() {
		defer s.backupBusy.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if s.hub != nil {
			s.hub.Progress("backup", "create", "Creating manual backup…")
		}
		e, err := s.createBackup(ctx)
		if err != nil {
			if s.hub != nil {
				s.hub.Error("backup", "Backup failed: "+err.Error())
			}
			s.logAction("backup", "manual backup failed: "+err.Error(), false)
			return
		}
		if s.hub != nil {
			s.hub.Done("backup", "Backup created: "+e.ID, true)
		}
		s.logAction("backup", "manual backup "+e.ID, true)
	}()
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

type deleteBatchReq struct {
	IDs []string `json:"ids"`
}

// handleBackupDeleteBatch deletes several backups in one call (the
// multi-select delete). Partial failure is reported per-ID, not all-or-
// nothing — a missing backup shouldn't block deleting the rest.
func (s *Server) handleBackupDeleteBatch(w http.ResponseWriter, r *http.Request) {
	if s.backupMgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "backups not available"})
		return
	}
	var req deleteBatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ids required"})
		return
	}
	failed := map[string]string{}
	deleted := 0
	for _, id := range req.IDs {
		if err := s.backupMgr.Delete(id); err != nil {
			failed[id] = err.Error()
		} else {
			deleted++
		}
	}
	detail := fmt.Sprintf("deleted %d backup(s)", deleted)
	if len(failed) > 0 {
		detail += fmt.Sprintf(", %d failed", len(failed))
	}
	s.recordAction("backup delete", detail, len(failed) == 0)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "failed": failed})
}

type restoreReq struct {
	ID        string `json:"id"`
	Broadcast string `json:"broadcast"`
	Delay     int    `json:"delay_seconds"`
}

// handleBackupRestore kicks a restore cycle in the background; 202, with
// progress on the live stream (same shape as the update cycle).
// handleServerReset wipes the world and starts fresh. Guarded by a typed
// confirmation word so it cannot be reached by a stray click or a
// half-considered curl.
func (s *Server) handleServerReset(w http.ResponseWriter, r *http.Request) {
	if s.reset == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "reset not available"})
		return
	}
	var req resetReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if req.Confirm != resetConfirmWord {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "confirmation word does not match — type " + resetConfirmWord + " exactly"})
		return
	}
	if !s.backupBusy.CompareAndSwap(false, true) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": errBackupBusy})
		return
	}
	go func() {
		defer s.backupBusy.Store(false)
		res := s.reset(context.Background(), ResetOptions{
			KeepSettings:   req.KeepSettings,
			WipePlayerData: req.WipePlayerData,
			Broadcast:      req.Broadcast,
			Delay:          req.Delay,
		})
		ok := res.Status == "success" || res.Status == "success_with_warnings"
		s.logAction("reset", res.Detail, ok)
	}()
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

type resetReq struct {
	Confirm        string `json:"confirm"`
	KeepSettings   bool   `json:"keep_settings"`
	WipePlayerData bool   `json:"wipe_player_data"`
	Broadcast      string `json:"broadcast"`
	Delay          int    `json:"delay_seconds"`
}

func (s *Server) handleBackupRestore(w http.ResponseWriter, r *http.Request) {
	if s.restore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "restore not available"})
		return
	}
	var req restoreReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}
	if !s.backupBusy.CompareAndSwap(false, true) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": errBackupBusy})
		return
	}
	go func() {
		defer s.backupBusy.Store(false)
		res := s.restore(context.Background(), req.ID, req.Broadcast, req.Delay)
		ok := res.Status == "success" || res.Status == "success_with_warnings"
		s.logAction("restore", res.Detail, ok)
	}()
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}
