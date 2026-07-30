package sav

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// fixture mirroring sav_cli's output shape.
const fixture = `{
  "players": [
    {"player_uid": "1234", "nickname": "Dexteradei", "level": 4, "exp": 120,
     "hp": 500, "max_hp": 500, "pals": [{"owner": "1234", "nickname": "Lamball", "level": 3}]}
  ],
  "guilds": [
    {"name": "Unnamed Guild", "base_camp_level": 2, "admin_player_uid": "1234",
     "players": [{"player_uid": "1234", "nickname": "Dexteradei", "last_online": "2026-07-30 21:14:02"}],
     "base_ids": ["9999"],
     "base_camp": [{"id": "9999", "state": 0, "transform": {"x": -351740, "y": 267486, "z": 6049.7}}]}
  ]
}`

// writeStubCLI writes a shell script that acts as sav_cli: verifies the -f
// file exists, writes the fixture to the -o path. Exercises the REAL exec
// path end to end.
func writeStubCLI(t *testing.T, dir string) string {
	t.Helper()
	cli := filepath.Join(dir, "sav_cli")
	script := `#!/bin/sh
# args: -f <level> -o <out>
while [ $# -gt 0 ]; do
  case "$1" in
    -f) LEVEL="$2"; shift 2;;
    -o) OUT="$2"; shift 2;;
    *) shift;;
  esac
done
[ -f "$LEVEL" ] || { echo "no level file" >&2; exit 1; }
cat > "$OUT" << 'JSON'
` + fixture + `
JSON
`
	if err := os.WriteFile(cli, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return cli
}

func testWorldDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Level.sav"), []byte("FAKE-SAV-BYTES"), 0o644)
	os.Mkdir(filepath.Join(dir, "Players"), 0o755)
	os.WriteFile(filepath.Join(dir, "Players", "0000.sav"), []byte("P"), 0o644)
	return dir
}

func TestRunnerParsesThroughStubCLI(t *testing.T) {
	world := testWorldDir(t)
	cli := writeStubCLI(t, t.TempDir())
	r := Runner{CLIPath: cli, WorldDir: world}
	w, err := r.Parse(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Players) != 1 || w.Players[0].Nickname != "Dexteradei" || w.Players[0].Level != 4 {
		t.Fatalf("player decode: %+v", w.Players)
	}
	if len(w.Guilds) != 1 || w.Guilds[0].Players[0].LastOnline == "" {
		t.Fatalf("guild decode (last_online is the offline-view payload): %+v", w.Guilds)
	}
	if w.Guilds[0].BaseCamp[0].Transform.X != -351740 {
		t.Fatalf("base transform: %+v", w.Guilds[0].BaseCamp)
	}
	if w.ParsedAt.IsZero() {
		t.Fatal("ParsedAt must be stamped")
	}
}

func TestRunnerMissingLevelSav(t *testing.T) {
	cli := writeStubCLI(t, t.TempDir())
	r := Runner{CLIPath: cli, WorldDir: t.TempDir()} // no Level.sav
	if _, err := r.Parse(context.Background()); err == nil {
		t.Fatal("missing Level.sav must error")
	}
}

func TestCachedTTLAndSingleFlight(t *testing.T) {
	var calls atomic.Int32
	slow := make(chan struct{})
	c := &Cached{TTL: time.Hour, Parse: func(ctx context.Context) (*World, error) {
		calls.Add(1)
		<-slow
		return &World{ParsedAt: time.Now()}, nil
	}}
	done := make(chan *World, 2)
	for i := 0; i < 2; i++ {
		go func() { w, _ := c.Get(context.Background()); done <- w }()
	}
	time.Sleep(50 * time.Millisecond)
	close(slow)
	<-done
	<-done
	if calls.Load() != 1 {
		t.Fatalf("concurrent gets must share one parse, got %d", calls.Load())
	}
	// Within TTL: cached, no new parse.
	c.Get(context.Background())
	if calls.Load() != 1 {
		t.Fatal("TTL cache must serve the snapshot")
	}
}

func TestCachedErrorAlsoCached(t *testing.T) {
	var calls atomic.Int32
	c := &Cached{TTL: time.Hour, Parse: func(ctx context.Context) (*World, error) {
		calls.Add(1)
		return nil, errors.New("boom")
	}}
	c.Get(context.Background())
	_, err := c.Get(context.Background())
	if err == nil || calls.Load() != 1 {
		t.Fatalf("errors cache too (no parse-retry hammering): calls=%d err=%v", calls.Load(), err)
	}
}
