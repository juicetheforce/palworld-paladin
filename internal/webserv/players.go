package webserv

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/juicetheforce/palworld-paladin/internal/palapi"
)

// PlayerProvider is the slice of palapi the Players page needs.
// *palapi.Client satisfies it.
type PlayerProvider interface {
	Players(ctx context.Context) ([]palapi.Player, error)
	Kick(ctx context.Context, userid, message string) error
	Ban(ctx context.Context, userid, message string) error
	Unban(ctx context.Context, userid string) error
}

// BanListReader returns the current ban list. Wired to read banlist.txt.
type BanListReader func() ([]palapi.BanEntry, error)

// rosterPlayer is one row in the Players page. Online players come from
// the live REST roster; the offline/guild/base fields are RESERVED for
// the future save-parsing tier (§6.5) — populated as null now so the UI
// can render placeholders in their final positions.
type rosterPlayer struct {
	Name   string  `json:"name"`
	UserID string  `json:"user_id"` // steam_<id> — moderation key
	Online bool    `json:"online"`
	Level  int     `json:"level"`
	Ping   float64 `json:"ping"`
	// Reserved for the save-parsing tier — always null/zero until then.
	Guild *string `json:"guild"` // null → "—" placeholder in UI
	Bases *int    `json:"bases"` // null → "—" placeholder in UI
}

type playersResponse struct {
	Online      bool           `json:"online"`       // is the game server reachable
	Players     []rosterPlayer `json:"players"`      // live roster (online tier)
	HistoryTier bool           `json:"history_tier"` // false until save parsing exists
	Error       string         `json:"error,omitempty"`
}

func (s *Server) handlePlayers(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var resp playersResponse
	resp.HistoryTier = false // no save parser yet (§6.5 historical tier deferred)

	live, err := s.players.Players(ctx)
	if err != nil {
		resp.Online = false
		resp.Error = "server unreachable: " + err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.Online = true
	for _, p := range live {
		resp.Players = append(resp.Players, rosterPlayer{
			Name: p.Name, UserID: p.UserID, Online: true,
			Level: p.Level, Ping: p.Ping,
			Guild: nil, Bases: nil, // reserved
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleBanList(w http.ResponseWriter, r *http.Request) {
	if s.banList == nil {
		writeJSON(w, http.StatusOK, map[string]any{"bans": []any{}})
		return
	}
	bans, err := s.banList()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if bans == nil {
		bans = []palapi.BanEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"bans": bans})
}

type modReq struct {
	UserID  string `json:"user_id"`
	Message string `json:"message"`
}

// handlePlayerAction dispatches kick/ban/unban. The action is the last
// path segment (/api/players/{action}).
func (s *Server) handlePlayerAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	var req modReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "user_id required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var err error
	switch action {
	case "kick":
		err = s.players.Kick(ctx, req.UserID, req.Message)
	case "ban":
		err = s.players.Ban(ctx, req.UserID, req.Message)
	case "unban":
		err = s.players.Unban(ctx, req.UserID)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown action"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
