package webserv

import (
	"context"
	"errors"
	"net/http"

	"github.com/juicetheforce/palworld-paladin/internal/sav"
)

// WorldFunc returns the parsed save snapshot (wired to sav.Cached in main).
type WorldFunc func(ctx context.Context) (*sav.World, error)

// handleWorld serves the historical tier (§6.5): players, guilds, and
// bases parsed from the save. Three honest states: parsed data; "sidecar
// not installed" with the setup hint; or a parse error.
func (s *Server) handleWorld(w http.ResponseWriter, r *http.Request) {
	if s.world == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "reason": "not configured"})
		return
	}
	world, err := s.world(r.Context())
	switch {
	case errors.Is(err, sav.ErrNoCLI):
		writeJSON(w, http.StatusOK, map[string]any{
			"available": false, "reason": "sidecar",
			"error": err.Error(),
		})
	case err != nil:
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "reason": "parse", "error": err.Error()})
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"available": true,
			"parsed_at": world.ParsedAt,
			"players":   world.Players,
			"guilds":    world.Guilds,
		})
	}
}
