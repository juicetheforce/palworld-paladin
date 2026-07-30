package webserv

import (
	"context"
	"net/http"
	"os"

	"github.com/juicetheforce/palworld-paladin/internal/palapi"
)

// World map (§6.5, rev 17): live actors from /game-data, converted to the
// in-game Paldex coordinate system. Transform constants and axis flip from
// palworld-coord (MIT, github.com/palworldlol/palworld-coord — thank you):
// translate, FLIP AXES (deliberate; the game does), scale by 459.
const (
	coordTranslX = 123888
	coordTranslY = 158000
	coordScale   = 459
)

// ActorsFunc reads the live actor snapshot (palapi Actors).
type ActorsFunc func(ctx context.Context) ([]palapi.Actor, error)

type mapActor struct {
	Kind    string  `json:"kind"` // "player" | "pal"
	Name    string  `json:"name"`
	Level   int     `json:"level"`
	HP      float64 `json:"hp"`
	MaxHP   float64 `json:"max_hp"`
	Species string  `json:"species,omitempty"`
	MapX    float64 `json:"map_x"` // Paldex coordinates
	MapY    float64 `json:"map_y"`
	Rot     float64 `json:"rot"`
}

func toMapCoords(savX, savY float64) (float64, float64) {
	// sav→Paldex: translate, flip axes, scale (palworld-coord).
	return (savY - coordTranslY) / coordScale, (savX + coordTranslX) / coordScale
}

func (s *Server) handleMapActors(w http.ResponseWriter, r *http.Request) {
	if s.actors == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	acts, err := s.actors(r.Context())
	if err != nil {
		// Flag missing or server down: the map shows "unavailable", not an error page.
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "error": err.Error()})
		return
	}
	out := make([]mapActor, 0, len(acts))
	for _, a := range acts {
		if a.IsActive != "" && a.IsActive != "true" {
			continue
		}
		ma := mapActor{
			Name: a.NickName, Level: a.Level, HP: a.HP, MaxHP: a.MaxHP, Rot: a.RotationZ,
		}
		ma.MapX, ma.MapY = toMapCoords(a.LocationX, a.LocationY)
		switch a.UnitType {
		case "Player":
			ma.Kind = "player"
		default:
			ma.Kind = "pal"
			ma.Species = a.NickName // NickName carries species for wild pals
		}
		out = append(out, ma)
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true, "actors": out})
}

// handleMapImage serves the operator-supplied map artwork, if present.
// Paladin deliberately bundles no game artwork (it is © Pocketpair); drop
// any map image at the configured path and it underlays the live radar.
func (s *Server) handleMapImage(w http.ResponseWriter, r *http.Request) {
	if s.mapImagePath == "" {
		http.NotFound(w, r)
		return
	}
	if _, err := os.Stat(s.mapImagePath); err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, s.mapImagePath)
}
