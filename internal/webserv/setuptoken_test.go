package webserv

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Regression support for the first-run setup token (audit 2026-09-29:
// /api/setup was unauthenticated). The token file must be private, stable
// until used, and compared strictly.

func TestEnsureSetupTokenCreatesPrivateStableToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "paladin-config", "setup-token")
	tok, created, err := EnsureSetupToken(path)
	if err != nil || !created {
		t.Fatalf("first ensure must create: created=%v err=%v", created, err)
	}
	if len(tok) != 32 {
		t.Fatalf("want a 32-char hex token, got %q", tok)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file must be 0600, got %v", fi.Mode().Perm())
	}
	// It never rotates while unused: a second ensure returns the same token.
	again, created, err := EnsureSetupToken(path)
	if err != nil || created || again != tok {
		t.Fatalf("second ensure must return the same token: %q created=%v err=%v", again, created, err)
	}
}

func TestCheckSetupToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "setup-token")
	if err := CheckSetupToken(path, "x"); !errors.Is(err, ErrNoSetupToken) {
		t.Fatalf("no file: want ErrNoSetupToken, got %v", err)
	}
	if err := CheckSetupToken("", "x"); !errors.Is(err, ErrNoSetupToken) {
		t.Fatalf("unconfigured path must fail closed, got %v", err)
	}
	tok, _, err := EnsureSetupToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckSetupToken(path, "  \n"); !errors.Is(err, ErrSetupTokenMissing) {
		t.Fatalf("blank: want ErrSetupTokenMissing, got %v", err)
	}
	if err := CheckSetupToken(path, tok[:31]+"x"); !errors.Is(err, ErrSetupTokenWrong) {
		t.Fatalf("wrong: want ErrSetupTokenWrong, got %v", err)
	}
	if err := CheckSetupToken(path, " "+tok+"\n"); err != nil {
		t.Fatalf("right token with stray whitespace must pass, got %v", err)
	}
	if err := RemoveSetupToken(path); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSetupToken(path); err != nil {
		t.Fatalf("removing a missing token must be fine, got %v", err)
	}
}
