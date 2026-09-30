package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/juicetheforce/palworld-paladin/internal/webserv"
)

// Regression: first-run /api/setup was unauthenticated (audit 2026-09-29).
// `paladin setup-token` is how the owner gets the token. Its stdout must be
// the token and nothing else, because the installer captures it.

func TestSetupTokenCmdNoAdmin(t *testing.T) {
	cfg := AppConfig{DataDir: t.TempDir()}
	var out, errOut bytes.Buffer
	if err := setupToken(cfg, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	tok := strings.TrimSpace(out.String())
	if len(tok) != 32 || out.String() != tok+"\n" {
		t.Fatalf("stdout must be exactly the token line, got %q", out.String())
	}
	if _, err := os.Stat(cfg.setupTokenFile()); err != nil {
		t.Fatalf("token file must exist next to auth.json: %v", err)
	}
	// Running it again (the "missed it" path) shows the same token.
	out.Reset()
	if err := setupToken(cfg, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != tok {
		t.Fatalf("rerun must print the same token: %q vs %q", out.String(), tok)
	}
}

func TestSetupTokenCmdAdminExists(t *testing.T) {
	cfg := AppConfig{DataDir: t.TempDir()}
	auth, err := webserv.LoadAuthStore(cfg.authFile())
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.SetAdminPassword("admin", "hunter2hunter2"); err != nil {
		t.Fatal(err)
	}
	// A leftover token (e.g. a failed delete after setup) gets tidied.
	if _, _, err := webserv.EnsureSetupToken(cfg.setupTokenFile()); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := setupToken(cfg, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("with an admin, nothing secret may be printed; stdout=%q", out.String())
	}
	if !strings.Contains(errOut.String(), "already complete") {
		t.Fatalf("must say setup is complete, got %q", errOut.String())
	}
	if _, err := os.Stat(cfg.setupTokenFile()); !os.IsNotExist(err) {
		t.Fatalf("leftover token file must be removed, stat err=%v", err)
	}
}
