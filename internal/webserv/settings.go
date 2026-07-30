package webserv

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/juicetheforce/palworld-paladin/internal/settings"
)

// The settings page (DESIGN.md §6.2/§6.3): a transactional editor over the
// verified key list. GET returns the full key metadata plus current ini
// values; POST /commit stages a diff and runs the commit-and-restart cycle
// on the serve engine. Values travel as strings both ways (they're form
// inputs); parsing/validation happens server-side against the key list —
// including the protected-key refusal (defense in depth: the UI greys
// them, the backend rejects them, and ValidateStaged is the enforcement).

// SettingsValuesFunc reads the current ini values (key → raw string).
type SettingsValuesFunc func() (map[string]string, error)

// CommitResult mirrors UpdateResult/RestoreResult for the commit cycle.
type CommitResult struct {
	Status string
	Detail string
}

// CommitRunner runs one commit cycle with the given staged (typed) diff.
type CommitRunner func(ctx context.Context, staged map[string]any, broadcast string, delaySec int) CommitResult

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	if s.keyList == nil || s.settingsValues == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "settings not available"})
		return
	}
	values, err := s.settingsValues()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read settings: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"keys":   s.keyList.Keys,
		"values": values,
	})
}

type commitReq struct {
	Changes   map[string]string `json:"changes"`
	Broadcast string            `json:"broadcast"`
	Delay     int               `json:"delay_seconds"`
}

func (s *Server) handleSettingsCommit(w http.ResponseWriter, r *http.Request) {
	if s.commit == nil || s.keyList == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "commit not available"})
		return
	}
	var req commitReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Changes) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "changes required"})
		return
	}

	// Parse string form values into typed values against the key list,
	// then validate the whole diff (types, ranges, protected keys).
	staged := make(map[string]any, len(req.Changes))
	for key, raw := range req.Changes {
		def, ok := s.keyList.Lookup(key)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown key: " + key})
			return
		}
		v, err := settings.ParseValue(def, raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("%s: %v", key, err)})
			return
		}
		staged[def.Key] = v
	}
	if err := s.keyList.ValidateStaged(staged); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if !s.commitBusy.CompareAndSwap(false, true) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "a commit is already running"})
		return
	}
	go func() {
		defer s.commitBusy.Store(false)
		res := s.commit(context.Background(), staged, req.Broadcast, req.Delay)
		ok := res.Status == "success" || res.Status == "success_with_warnings"
		s.logAction("settings commit", res.Detail, ok)
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "staged": len(staged)})
}
