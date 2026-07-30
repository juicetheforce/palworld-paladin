package webserv

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/juicetheforce/palworld-paladin/internal/events"
	"github.com/juicetheforce/palworld-paladin/internal/palapi"
	"github.com/juicetheforce/palworld-paladin/internal/settings"
)

// StatusProvider is the slice of palapi the dashboard needs. *palapi.Client
// satisfies it. Kept as an interface so the server is testable without a
// live game server.
type StatusProvider interface {
	Info(ctx context.Context) (*palapi.Info, error)
	Metrics(ctx context.Context) (*palapi.Metrics, error)
}

// BackupCounter reports how many backups exist (for the dashboard card).
type BackupCounter interface {
	Count() (int, error)
}

// HostProvider returns the latest host snapshot (CPU/RAM/temps/network).
// Implemented by a background sampler so throughput deltas stay warm
// regardless of how often the browser polls (DESIGN.md §6.6a).
type HostProvider interface {
	Latest() any
}

// Server is Paladin's HTTP server: JSON API under /api, static SPA
// everything else. LAN/localhost bind by default (§8).
type Server struct {
	auth           *AuthStore
	sessions       *SessionStore
	status         StatusProvider
	backups        BackupCounter
	host           HostProvider
	players        PlayerProvider
	banList        BanListReader
	lifecycle      Lifecycle
	broadcaster    Broadcaster
	backupMgr      BackupManager
	readiness      Readiness
	update         UpdateRunner
	updateBusy     atomic.Bool
	localBuild     LocalBuildFunc
	remoteBuild    RemoteBuildFunc
	memRestart     MemRestartStore
	unitMemory     UnitMemoryFunc
	createBackup   CreateBackupFunc
	restore        RestoreRunner
	backupBusy     atomic.Bool
	keyList        *settings.KeyList
	settingsValues SettingsValuesFunc
	commit         CommitRunner
	commitBusy     atomic.Bool
	logTail        func(n int) ([]string, error)
	gameTime       func(ctx context.Context) (string, int, bool)
	actors         ActorsFunc
	mapImagePath   string
	check          updateCheck
	actions        *actionLog
	hub            *events.Hub
	static         fs.FS // the embedded built React bundle
	mux            *http.ServeMux
}

// Config wires a Server.
type Config struct {
	Auth           *AuthStore
	Sessions       *SessionStore
	Status         StatusProvider
	Backups        BackupCounter
	Host           HostProvider
	Players        PlayerProvider
	BanList        BanListReader
	Lifecycle      Lifecycle
	Broadcaster    Broadcaster
	BackupMgr      BackupManager
	Readiness      Readiness
	Update         UpdateRunner
	LocalBuild     LocalBuildFunc
	RemoteBuild    RemoteBuildFunc
	MemRestart     MemRestartStore
	UnitMemory     UnitMemoryFunc
	CreateBackup   CreateBackupFunc
	Restore        RestoreRunner
	KeyList        *settings.KeyList
	SettingsValues SettingsValuesFunc
	Commit         CommitRunner
	LogTail        func(n int) ([]string, error)
	GameTime       func(ctx context.Context) (string, int, bool)
	Actors         ActorsFunc
	MapImagePath   string
	Hub            *events.Hub
	Static         fs.FS
}

func New(cfg Config) *Server {
	s := &Server{
		auth: cfg.Auth, sessions: cfg.Sessions, status: cfg.Status,
		backups: cfg.Backups, host: cfg.Host, players: cfg.Players,
		banList: cfg.BanList, lifecycle: cfg.Lifecycle,
		broadcaster: cfg.Broadcaster, backupMgr: cfg.BackupMgr, readiness: cfg.Readiness,
		update:     cfg.Update,
		localBuild: cfg.LocalBuild, remoteBuild: cfg.RemoteBuild,
		memRestart: cfg.MemRestart, unitMemory: cfg.UnitMemory,
		createBackup: cfg.CreateBackup, restore: cfg.Restore,
		keyList: cfg.KeyList, settingsValues: cfg.SettingsValues, commit: cfg.Commit,
		logTail: cfg.LogTail, gameTime: cfg.GameTime,
		actors: cfg.Actors, mapImagePath: cfg.MapImagePath,
		actions: newActionLog(100), hub: cfg.Hub, static: cfg.Static, mux: http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	// Auth-related (no session required).
	s.mux.HandleFunc("POST /api/setup", s.handleSetup)
	s.mux.HandleFunc("POST /api/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/logout", s.handleLogout)
	s.mux.HandleFunc("GET /api/session", s.handleSession)

	// Protected API.
	s.mux.Handle("GET /api/status", s.requireAuth(http.HandlerFunc(s.handleStatus)))
	s.mux.Handle("GET /api/host", s.requireAuth(http.HandlerFunc(s.handleHost)))
	s.mux.Handle("GET /api/players", s.requireAuth(http.HandlerFunc(s.handlePlayers)))
	s.mux.Handle("GET /api/bans", s.requireAuth(http.HandlerFunc(s.handleBanList)))
	s.mux.Handle("POST /api/players/{action}", s.requireAuth(http.HandlerFunc(s.handlePlayerAction)))
	s.mux.Handle("POST /api/admin/lifecycle/{action}", s.requireAuth(http.HandlerFunc(s.handleLifecycle)))
	s.mux.Handle("POST /api/admin/broadcast", s.requireAuth(http.HandlerFunc(s.handleBroadcast)))
	s.mux.Handle("POST /api/admin/save", s.requireAuth(http.HandlerFunc(s.handleSave)))
	s.mux.Handle("GET /api/admin/backups", s.requireAuth(http.HandlerFunc(s.handleBackupList)))
	s.mux.Handle("DELETE /api/admin/backups/{id}", s.requireAuth(http.HandlerFunc(s.handleBackupDelete)))
	s.mux.Handle("GET /api/admin/history", s.requireAuth(http.HandlerFunc(s.handleHistory)))
	s.mux.Handle("GET /api/events", s.requireAuth(http.HandlerFunc(s.handleEvents)))
	s.mux.Handle("GET /api/events/recent", s.requireAuth(http.HandlerFunc(s.handleEventsRecent)))
	s.mux.Handle("GET /api/admin/map-actors", s.requireAuth(http.HandlerFunc(s.handleMapActors)))
	s.mux.Handle("GET /api/map-image", s.requireAuth(http.HandlerFunc(s.handleMapImage)))
	s.mux.Handle("POST /api/admin/update", s.requireAuth(http.HandlerFunc(s.handleUpdate)))
	s.mux.Handle("GET /api/admin/update-check", s.requireAuth(http.HandlerFunc(s.handleUpdateCheckGet)))
	s.mux.Handle("POST /api/admin/update-check", s.requireAuth(http.HandlerFunc(s.handleUpdateCheckRefresh)))
	s.mux.Handle("GET /api/admin/mem-restart", s.requireAuth(http.HandlerFunc(s.handleMemRestartGet)))
	s.mux.Handle("PUT /api/admin/mem-restart", s.requireAuth(http.HandlerFunc(s.handleMemRestartSet)))
	s.mux.Handle("POST /api/admin/backups", s.requireAuth(http.HandlerFunc(s.handleBackupCreate)))
	s.mux.Handle("POST /api/admin/backups/delete-batch", s.requireAuth(http.HandlerFunc(s.handleBackupDeleteBatch)))
	s.mux.Handle("POST /api/admin/backups/restore", s.requireAuth(http.HandlerFunc(s.handleBackupRestore)))
	s.mux.Handle("GET /api/admin/settings", s.requireAuth(http.HandlerFunc(s.handleSettingsGet)))
	s.mux.Handle("POST /api/admin/settings/commit", s.requireAuth(http.HandlerFunc(s.handleSettingsCommit)))

	// Static SPA fallback for everything else.
	if s.static != nil {
		s.mux.Handle("/", s.spaHandler())
	}
}

// ---- helpers ---------------------------------------------------------------

const cookieName = "paladin_session"

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		if _, ok := s.sessions.Valid(c.Value); !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session expired"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, tok string) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: tok, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		// Secure is intentionally NOT set: LAN/HTTP deployment. Behind a
		// TLS reverse proxy the operator can add it; documented in §8.
	})
}

// ---- handlers --------------------------------------------------------------

type credsReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleSetup creates the first admin credential (only when none exists).
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !s.auth.NeedsSetup() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already configured"})
		return
	}
	var req credsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	if req.Username == "" {
		req.Username = "admin"
	}
	if err := s.auth.SetAdminPassword(req.Username, req.Password); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.issueSession(w, req.Username)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req credsReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
		return
	}
	// Single-admin model: the UI sends only a password; the username is a
	// fixed internal constant (§6.6). Default it so password-only login
	// works, while the users-slice storage stays RBAC-ready.
	if req.Username == "" {
		req.Username = "admin"
	}
	if !s.auth.Verify(req.Username, req.Password) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}
	s.issueSession(w, req.Username)
}

func (s *Server) issueSession(w http.ResponseWriter, username string) {
	tok, err := s.sessions.New(username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "session"})
		return
	}
	s.setSessionCookie(w, tok)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": username})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSession tells the frontend which screen to show: setup, login, or
// the app.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if s.auth.NeedsSetup() {
		writeJSON(w, http.StatusOK, map[string]any{"state": "needs_setup"})
		return
	}
	if c, err := r.Cookie(cookieName); err == nil {
		if user, ok := s.sessions.Valid(c.Value); ok {
			writeJSON(w, http.StatusOK, map[string]any{"state": "authenticated", "username": user})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": "needs_login"})
}

// StatusResponse is the dashboard payload (read-only; §6.5 live tier).
type StatusResponse struct {
	InGameTime  string  `json:"in_game_time,omitempty"`
	InGameDays  int     `json:"in_game_days,omitempty"`
	ServerName  string  `json:"server_name"`
	Description string  `json:"description"`
	Version     string  `json:"version"`
	WorldGUID   string  `json:"world_guid"`
	Online      bool    `json:"online"`
	FPS         float64 `json:"fps"`
	FPSAverage  float64 `json:"fps_average"`
	FrameTime   float64 `json:"frame_time_ms"`
	Players     int     `json:"players"`
	MaxPlayers  int     `json:"max_players"`
	Bases       int     `json:"bases"`
	Days        int     `json:"days"`
	UptimeSec   int     `json:"uptime_sec"`
	BackupCount int     `json:"backup_count"`
	Error       string  `json:"error,omitempty"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var resp StatusResponse
	info, err := s.status.Info(ctx)
	if err != nil {
		// Server unreachable is a normal, reportable state — not an HTTP
		// error. The dashboard shows "offline" rather than breaking.
		resp.Online = false
		resp.Error = "server unreachable: " + err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.Online = true
	resp.ServerName, resp.Description = info.ServerName, info.Description
	resp.Version, resp.WorldGUID = info.Version, info.WorldGUID

	if s.gameTime != nil {
		if t, days, ok := s.gameTime(ctx); ok {
			resp.InGameTime, resp.InGameDays = t, days
		}
	}
	if m, err := s.status.Metrics(ctx); err == nil {
		resp.FPS, resp.FPSAverage = m.ServerFPS, m.ServerFPSAverage
		resp.FrameTime = m.ServerFrameTime
		resp.Players, resp.MaxPlayers = m.CurrentPlayerNum, m.MaxPlayerNum
		resp.Bases, resp.Days, resp.UptimeSec = m.BaseCampNum, m.Days, m.Uptime
	}
	if s.backups != nil {
		if n, err := s.backups.Count(); err == nil {
			resp.BackupCount = n
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleHost returns the latest host-metrics snapshot. If no host provider
// is wired, it returns an explicit unavailable marker rather than 404, so
// the dashboard can hide host cards cleanly.
func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	if s.host == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	writeJSON(w, http.StatusOK, s.host.Latest())
}

// spaHandler serves embedded static files, falling back to index.html for
// client-side routes (single-page app).
func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(s.static, cleanPath(r.URL.Path)); errors.Is(err, fs.ErrNotExist) {
			// Unknown non-API path → serve index.html for the SPA router.
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}

func cleanPath(p string) string {
	if p == "/" || p == "" {
		return "index.html"
	}
	return p[1:] // strip leading slash for fs.FS lookups
}
