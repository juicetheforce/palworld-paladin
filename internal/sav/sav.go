// Package sav integrates the save-file sidecar (DESIGN.md §5.1/§6.5): a
// separate sav_cli process (PST's packaged parser, Apache-2.0 wrapper over
// GPL-3.0 Oodle-decompression deps) parses Level.sav into JSON, and this
// package stages, execs, decodes, and caches. The PROCESS BOUNDARY is the
// point: Paladin never links the GPL components — it runs a program and
// reads its output, keeping Paladin's own licensing untouched. The sidecar
// binary is acquired at deploy time (like SteamCMD), never vendored.
package sav

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// ---- JSON schema (matches sav_cli's world_types to_dict order) ----

type Pal struct {
	Owner    string `json:"owner"`
	Nickname string `json:"nickname"`
	Level    int    `json:"level"`
}

type Player struct {
	PlayerUID string  `json:"player_uid"`
	Nickname  string  `json:"nickname"`
	Level     int     `json:"level"`
	Exp       int     `json:"exp"`
	HP        float64 `json:"hp"`
	MaxHP     float64 `json:"max_hp"`
	Pals      []Pal   `json:"pals"`
}

type GuildMember struct {
	PlayerUID  string `json:"player_uid"`
	Nickname   string `json:"nickname"`
	LastOnline string `json:"last_online"` // local time string from sav_cli
}

type Transform struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

type BaseCamp struct {
	ID        string    `json:"id"`
	State     int       `json:"state"`
	Transform Transform `json:"transform"`
}

type Guild struct {
	Name           string        `json:"name"`
	BaseCampLevel  int           `json:"base_camp_level"`
	AdminPlayerUID string        `json:"admin_player_uid"`
	Players        []GuildMember `json:"players"`
	BaseIDs        []string      `json:"base_ids"`
	BaseCamp       []BaseCamp    `json:"base_camp"`
}

// World is one parsed snapshot of the save.
type World struct {
	Players  []Player  `json:"players"`
	Guilds   []Guild   `json:"guilds"`
	ParsedAt time.Time `json:"parsed_at"`
}

// ---- sidecar discovery ----

// ErrNoCLI: the sidecar binary is not installed. The UI presents this as
// a setup hint, not a failure.
var ErrNoCLI = errors.New("sav_cli not installed")

// FindCLI locates the sav_cli binary: PALADIN_SAV_CLI env override, then
// beside the paladin executable, then the conventional tools dir.
func FindCLI() (string, error) {
	if p := os.Getenv("PALADIN_SAV_CLI"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
		return "", fmt.Errorf("sav: PALADIN_SAV_CLI=%q does not exist", p)
	}
	if exe, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(exe), "sav_cli")
		if _, err := os.Stat(cand); err == nil {
			return cand, nil
		}
	}
	cand := "/home/palworld/paladin-tools/sav_cli"
	if _, err := os.Stat(cand); err == nil {
		return cand, nil
	}
	return "", fmt.Errorf("%w (set PALADIN_SAV_CLI, or place it at /home/palworld/paladin-tools/sav_cli)", ErrNoCLI)
}

// ---- runner ----

// Runner stages a copy of the save, runs the sidecar, decodes its JSON.
type Runner struct {
	CLIPath  string
	WorldDir string // .../SaveGames/0/<GUID>
	Timeout  time.Duration
}

// Parse runs one full parse. The save files are COPIED to a temp dir
// first: Level.sav is rewritten by the game on every autosave, and
// parsing a file mid-write must never be possible.
func (r Runner) Parse(ctx context.Context) (*World, error) {
	if r.Timeout <= 0 {
		r.Timeout = 3 * time.Minute
	}
	stage, err := os.MkdirTemp("", "paladin-sav-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)

	level := filepath.Join(r.WorldDir, "Level.sav")
	stagedLevel := filepath.Join(stage, "Level.sav")
	if err := copyFile(level, stagedLevel); err != nil {
		return nil, fmt.Errorf("sav: stage Level.sav: %w", err)
	}
	// Per-player saves enrich player records; copy if present (small files).
	if entries, err := os.ReadDir(filepath.Join(r.WorldDir, "Players")); err == nil {
		os.Mkdir(filepath.Join(stage, "Players"), 0o755)
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			copyFile(filepath.Join(r.WorldDir, "Players", e.Name()),
				filepath.Join(stage, "Players", e.Name()))
		}
	}

	out := filepath.Join(stage, "structure.json")
	cctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, r.CLIPath, "-f", stagedLevel, "-o", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("sav: sav_cli failed: %w (output tail: %s)", err, tail(string(b), 300))
	}

	b, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("sav: sav_cli produced no output file: %w", err)
	}
	var w World
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, fmt.Errorf("sav: decode structure.json: %w", err)
	}
	w.ParsedAt = time.Now()
	return &w, nil
}

// ---- cache (parse is expensive; page loads are not) ----

// Cached wraps a parse func with a TTL and single-flight: concurrent
// requests share one parse, and repeats within the TTL get the snapshot.
type Cached struct {
	Parse func(ctx context.Context) (*World, error)
	TTL   time.Duration

	mu      sync.Mutex
	last    *World
	lastErr error
	at      time.Time
	flight  chan struct{} // non-nil while a parse is running
}

func (c *Cached) Get(ctx context.Context) (*World, error) {
	c.mu.Lock()
	ttl := c.TTL
	if ttl <= 0 {
		ttl = time.Minute
	}
	if time.Since(c.at) < ttl && (c.last != nil || c.lastErr != nil) {
		w, err := c.last, c.lastErr
		c.mu.Unlock()
		return w, err
	}
	if c.flight != nil {
		ch := c.flight
		c.mu.Unlock()
		select {
		case <-ch: // the in-flight parse finished; serve its result
			c.mu.Lock()
			w, err := c.last, c.lastErr
			c.mu.Unlock()
			return w, err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ch := make(chan struct{})
	c.flight = ch
	c.mu.Unlock()

	w, err := c.Parse(ctx)

	c.mu.Lock()
	c.last, c.lastErr, c.at = w, err, time.Now()
	c.flight = nil
	close(ch)
	c.mu.Unlock()
	return w, err
}

// ---- helpers ----

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
