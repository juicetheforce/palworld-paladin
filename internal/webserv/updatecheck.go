package webserv

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Update-availability check (DESIGN.md §6.4): the card shows whether a new
// server build exists BEFORE the operator commits to the update cycle.
//
// Two-speed design: the installed buildid is a local manifest read (instant,
// done fresh on every GET), while Steam's public buildid needs a steamcmd
// run (30-90s), so the remote side is cached with a staleness window and
// refreshed in the background — triggered by a human loading the page, not
// by any timer. No recurring background job exists; checks only ever
// happen because someone looked or clicked.

// LocalBuildFunc reads the installed buildid (fast, local).
type LocalBuildFunc func() (string, error)

// RemoteBuildFunc queries Steam's public-branch buildid (slow, network).
type RemoteBuildFunc func(ctx context.Context) (string, error)

// checkStaleness: a cached remote result older than this triggers a lazy
// background refresh on page load.
const checkStaleness = time.Hour

type updateCheck struct {
	mu        sync.Mutex
	checking  bool
	checkedAt time.Time
	remote    string
	errText   string
}

type updateCheckResponse struct {
	LocalBuildID    string     `json:"local_buildid"`
	RemoteBuildID   string     `json:"remote_buildid,omitempty"`
	UpdateAvailable bool       `json:"update_available"`
	CheckedAt       *time.Time `json:"checked_at,omitempty"`
	Checking        bool       `json:"checking"`
	Error           string     `json:"error,omitempty"`
}

// handleUpdateCheckGet returns the current picture and lazily kicks a
// background refresh when the cached remote result is stale or absent.
func (s *Server) handleUpdateCheckGet(w http.ResponseWriter, r *http.Request) {
	if s.localBuild == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	local, lerr := s.localBuild()

	s.check.mu.Lock()
	stale := s.check.checkedAt.IsZero() || time.Since(s.check.checkedAt) > checkStaleness
	if stale && !s.check.checking && s.remoteBuild != nil {
		s.check.checking = true
		go s.runRemoteCheck()
	}
	resp := updateCheckResponse{
		LocalBuildID: local,
		Checking:     s.check.checking,
		Error:        s.check.errText,
	}
	if !s.check.checkedAt.IsZero() {
		t := s.check.checkedAt
		resp.CheckedAt = &t
		resp.RemoteBuildID = s.check.remote
		resp.UpdateAvailable = s.check.remote != "" && local != "" && s.check.remote != local
	}
	s.check.mu.Unlock()

	if lerr != nil && resp.Error == "" {
		resp.Error = "installed buildid: " + lerr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleUpdateCheckRefresh forces a fresh remote check (the "Check now"
// button). 202 if kicked or already in flight.
func (s *Server) handleUpdateCheckRefresh(w http.ResponseWriter, r *http.Request) {
	if s.remoteBuild == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "update check not available"})
		return
	}
	s.check.mu.Lock()
	if !s.check.checking {
		s.check.checking = true
		go s.runRemoteCheck()
	}
	s.check.mu.Unlock()
	writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
}

func (s *Server) runRemoteCheck() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	remote, err := s.remoteBuild(ctx)

	s.check.mu.Lock()
	s.check.checking = false
	s.check.checkedAt = time.Now()
	if err != nil {
		s.check.errText = err.Error()
	} else {
		s.check.errText = ""
		s.check.remote = remote
	}
	s.check.mu.Unlock()
}
