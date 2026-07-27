//go:build integration

// Integration tests against a LIVE Palworld server (the test box).
// These are excluded from normal builds and CI by the build tag.
//
// Run on the test box:
//
//	PW=$(sudo grep -oP 'AdminPassword: \K\S+' /home/palworld/palserver-credentials.txt)
//	PALWORLD_ADMIN_PASSWORD=$PW go test -tags=integration -v ./internal/palapi/
//
// Optional: PALWORLD_API_URL (default http://127.0.0.1:8212).
//
// Write actions used: Announce, Save, Ban+Unban of a synthetic
// never-connected ID (verified harmless on 2026-07-27). Shutdown/Stop are
// deliberately NOT exercised.
package palapi

import (
	"context"
	"os"
	"testing"
	"time"
)

func liveClient(t *testing.T) *Client {
	t.Helper()
	pw := os.Getenv("PALWORLD_ADMIN_PASSWORD")
	if pw == "" {
		t.Skip("PALWORLD_ADMIN_PASSWORD not set; skipping live integration tests")
	}
	url := os.Getenv("PALWORLD_API_URL")
	if url == "" {
		url = "http://127.0.0.1:8212"
	}
	return New(url, pw)
}

func TestLiveInfoAndReadiness(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.WaitReady(ctx, time.Second); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	info, err := c.Info(ctx)
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	t.Logf("server: %s (%q, world %s)", info.Version, info.ServerName, info.WorldGUID)
	if info.Version == "" || info.WorldGUID == "" {
		t.Fatalf("suspicious empty fields: %+v", info)
	}
}

func TestLiveReadEndpoints(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()

	players, err := c.Players(ctx)
	if err != nil {
		t.Fatalf("Players: %v", err)
	}
	t.Logf("players online: %d", len(players))

	settings, err := c.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	t.Logf("settings keys returned: %d", len(settings))
	if len(settings) == 0 {
		t.Fatal("settings readback returned zero keys — VERIFY step depends on this")
	}

	metrics, err := c.Metrics(ctx)
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}
	// Capture the real field names for the typed-getter follow-up.
	t.Logf("metrics payload (capture these field names): %v", metrics)
}

func TestLiveGameDataProbe(t *testing.T) {
	c := liveClient(t)
	available, err := c.ProbeGameData(context.Background())
	if err != nil {
		t.Fatalf("ProbeGameData: %v", err)
	}
	t.Logf("/game-data available on this build: %v", available)
	// No assertion on the value: absence is expected on v1.0.1.100619, but
	// a future patch making it appear is a finding, not a failure.
}

func TestLiveWriteActions(t *testing.T) {
	c := liveClient(t)
	ctx := context.Background()

	if err := c.Announce(ctx, "palapi integration check"); err != nil {
		t.Fatalf("Announce: %v", err)
	}
	if err := c.Save(ctx); err != nil {
		t.Fatalf("Save: %v", err)
	}

	const fake = "steam_00000000000000001"
	if err := c.Ban(ctx, fake, "palapi integration check"); err != nil {
		t.Fatalf("Ban: %v", err)
	}
	if err := c.Unban(ctx, fake); err != nil {
		t.Fatalf("Unban (IMPORTANT: banlist may still contain %s): %v", fake, err)
	}
}
