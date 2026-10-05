package main

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/juicetheforce/palworld-paladin/internal/maintain"
	"github.com/juicetheforce/palworld-paladin/internal/webserv"
)

// `paladin reset-password` replaces the old manual recovery (delete
// auth.json by hand and restart), which the 2026-10-04 fresh-install test
// showed nobody could be expected to find.

// withAdmin returns a config whose data dir holds an admin account.
func withAdmin(t *testing.T) AppConfig {
	t.Helper()
	cfg := AppConfig{DataDir: t.TempDir()}
	auth, err := webserv.LoadAuthStore(cfg.authFile())
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.SetAdminPassword("admin", "hunter2hunter2"); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestResetPasswordConfirmed(t *testing.T) {
	cfg := withAdmin(t)
	restarted := 0
	var out, errOut bytes.Buffer
	err := resetPassword(cfg, strings.NewReader("y\n"), &out, &errOut, func() error { restarted++; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.authFile()); !os.IsNotExist(err) {
		t.Fatalf("admin account must be removed, stat err=%v", err)
	}
	tok := strings.TrimSpace(out.String())
	if len(tok) != 32 || out.String() != tok+"\n" {
		t.Fatalf("stdout must be exactly the new setup token, got %q", out.String())
	}
	if err := webserv.CheckSetupToken(cfg.setupTokenFile(), tok); err != nil {
		t.Fatalf("printed token must be the stored one: %v", err)
	}
	// Restart is required: serve holds the admin and sessions in memory.
	if restarted != 1 {
		t.Fatalf("Paladin must be restarted exactly once, got %d", restarted)
	}
}

func TestResetPasswordDeclined(t *testing.T) {
	cfg := withAdmin(t)
	for _, answer := range []string{"n\n", "\n", "", "maybe\n"} {
		restarted := 0
		var out, errOut bytes.Buffer
		if err := resetPassword(cfg, strings.NewReader(answer), &out, &errOut, func() error { restarted++; return nil }); err != nil {
			t.Fatal(err)
		}
		if out.Len() != 0 || restarted != 0 {
			t.Fatalf("answer %q: nothing may happen (stdout=%q restarts=%d)", answer, out.String(), restarted)
		}
		auth, _ := webserv.LoadAuthStore(cfg.authFile())
		if !auth.Verify("admin", "hunter2hunter2") {
			t.Fatalf("answer %q: the admin account must be untouched", answer)
		}
	}
}

func TestResetPasswordNoAdmin(t *testing.T) {
	cfg := AppConfig{DataDir: t.TempDir()}
	restarted := 0
	var out, errOut bytes.Buffer
	// No confirmation needed: there's nothing to remove.
	if err := resetPassword(cfg, strings.NewReader(""), &out, &errOut, func() error { restarted++; return nil }); err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(out.String())) != 32 {
		t.Fatalf("with no admin, the setup token must be printed, got %q", out.String())
	}
	if !strings.Contains(errOut.String(), "nothing to reset") {
		t.Fatalf("must say there's nothing to reset, got %q", errOut.String())
	}
	if restarted != 0 {
		t.Fatal("no restart when there was no admin")
	}
}

func TestResetPasswordRefusesDuringCycle(t *testing.T) {
	cfg := withAdmin(t)
	j, err := maintain.NewFileJournal(cfg.journalDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Begin("commit-1", "commit"); err != nil {
		t.Fatal(err)
	}
	restarted := 0
	var out, errOut bytes.Buffer
	err = resetPassword(cfg, strings.NewReader("y\n"), &out, &errOut, func() error { restarted++; return nil })
	if err == nil || !strings.Contains(err.Error(), "maintenance cycle") {
		t.Fatalf("must refuse while a cycle is open, got %v", err)
	}
	if restarted != 0 || out.Len() != 0 {
		t.Fatal("nothing may happen while a cycle is open")
	}
	if _, err := os.Stat(cfg.authFile()); err != nil {
		t.Fatalf("admin account must be untouched: %v", err)
	}
}

func TestResetPasswordRestartFails(t *testing.T) {
	cfg := withAdmin(t)
	var out, errOut bytes.Buffer
	err := resetPassword(cfg, strings.NewReader("yes\n"), &out, &errOut, func() error { return errors.New("no systemd here") })
	if err == nil {
		t.Fatal("a failed restart must be reported as an error")
	}
	// The account is gone either way, so the operator still needs the token.
	if len(strings.TrimSpace(out.String())) != 32 {
		t.Fatalf("token must still be printed, got %q", out.String())
	}
	if !strings.Contains(errOut.String(), "systemctl restart paladin") {
		t.Fatalf("must tell the operator how to restart, got %q", errOut.String())
	}
}
