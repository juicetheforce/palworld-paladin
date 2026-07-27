package palapi

import (
	"context"
	"encoding/json"
)

// Typing philosophy (deliberate, DESIGN.md §6.2 ethos): endpoints whose
// shape we have verified live get typed structs (Info, Players). Endpoints
// whose field sets shift with game patches (settings) or that we have not
// yet observed live (metrics fields, game-data) decode into maps /
// RawMessage so a game update degrades gracefully instead of silently
// zeroing struct fields. Typed accessors get added as shapes are verified
// on the test box — never from documentation alone.

// Info is the /info response. Shape verified live on v1.0.1.100619.
type Info struct {
	Version     string `json:"version"`
	ServerName  string `json:"servername"`
	Description string `json:"description"`
	WorldGUID   string `json:"worldguid"`
}

// Info returns server identity and version. Also the readiness probe.
func (c *Client) Info(ctx context.Context) (*Info, error) {
	var out Info
	if err := c.get(ctx, "/info", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Player is one entry from /players. Fields are the stable, long-known
// set; unknown/new fields are ignored by encoding/json (use PlayersRaw
// when forward-compat access to everything is needed).
type Player struct {
	Name      string  `json:"name"`
	PlayerID  string  `json:"playerId"`
	UserID    string  `json:"userId"` // steam_<id> — the moderation identifier
	IP        string  `json:"ip"`
	Ping      float64 `json:"ping"`
	LocationX float64 `json:"location_x"`
	LocationY float64 `json:"location_y"`
	Level     int     `json:"level"`
}

type playersEnvelope struct {
	Players []Player `json:"players"`
}

// Players returns the live roster of connected players.
func (c *Client) Players(ctx context.Context) ([]Player, error) {
	var out playersEnvelope
	if err := c.get(ctx, "/players", &out); err != nil {
		return nil, err
	}
	return out.Players, nil
}

// PlayersRaw returns the /players response undecoded, for callers that
// need fields beyond the stable Player set.
func (c *Client) PlayersRaw(ctx context.Context) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.get(ctx, "/players", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Settings returns the server's EFFECTIVE settings as a key→value map.
// Read-only readback — this is what the commit workflow's VERIFY step
// compares against the staged diff (DESIGN.md §6.3 step 8). A map, not a
// struct: the key set moves with game patches and Paladin's key list is
// data-driven, so the client must not hardcode a shape.
func (c *Client) Settings(ctx context.Context) (map[string]any, error) {
	out := map[string]any{}
	if err := c.get(ctx, "/settings", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Metrics returns server performance metrics as a map (fps, player count,
// frame time, uptime, …). Map until the exact 1.0.1 field names are
// captured live on the test box; typed getters follow that capture.
func (c *Client) Metrics(ctx context.Context) (map[string]any, error) {
	out := map[string]any{}
	if err := c.get(ctx, "/metrics", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GameData returns the raw /game-data world actor snapshot. On builds
// where the endpoint is absent (currently v1.0.1.100619) the error
// matches ErrNotAvailable; see ProbeGameData.
func (c *Client) GameData(ctx context.Context) (json.RawMessage, error) {
	var out json.RawMessage
	if err := c.get(ctx, "/game-data", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Announce broadcasts a message to all players. There is no per-player
// message endpoint on this API (verified; DESIGN.md §6.7).
func (c *Client) Announce(ctx context.Context, message string) error {
	return c.post(ctx, "/announce", map[string]string{"message": message})
}

// Kick disconnects a connected player (rejoinable). userid is the
// steam_<id> form from Players().
func (c *Client) Kick(ctx context.Context, userid, message string) error {
	return c.post(ctx, "/kick", map[string]string{"userid": userid, "message": message})
}

// Ban bans a player and persists to Pal/Saved/SaveGames/banlist.txt
// (line format: "steam_<id>,<opaque-32-hex>"). Verified to accept IDs
// that have never connected — offline bans work (DESIGN.md §6.7).
func (c *Client) Ban(ctx context.Context, userid, message string) error {
	return c.post(ctx, "/ban", map[string]string{"userid": userid, "message": message})
}

// Unban removes a ban. Verified to blank the banlist.txt entry (an empty
// file is an empty list, not an error).
func (c *Client) Unban(ctx context.Context, userid string) error {
	return c.post(ctx, "/unban", map[string]string{"userid": userid})
}

// Save forces a world save and returns when the server has acknowledged.
// (~40 ms on an idle world, measured.) Step 3 of the maintenance cycle.
func (c *Client) Save(ctx context.Context) error {
	return c.post(ctx, "/save", nil)
}

// Shutdown requests a graceful shutdown after waitSeconds, showing message
// to players. NOTE: Paladin's maintenance cycles stop the server via its
// systemd unit instead (DESIGN.md §6.3/§6.9) so the supervisor stays the
// single source of truth about process state; this endpoint exists for
// completeness and ad-hoc use.
func (c *Client) Shutdown(ctx context.Context, waitSeconds int, message string) error {
	return c.post(ctx, "/shutdown", map[string]any{"waittime": waitSeconds, "message": message})
}

// Stop force-stops the server immediately (no countdown, no announce).
// Same note as Shutdown: the maintenance state machine does not use this.
func (c *Client) Stop(ctx context.Context) error {
	return c.post(ctx, "/stop", nil)
}
