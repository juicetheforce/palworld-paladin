package webserv

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// First-run setup token.
//
// Why this exists: the installer makes the UI listen on all interfaces, and
// until an admin exists POST /api/setup used to accept anyone. On a fresh
// install, whoever reached the port first could claim the admin account
// (audit 2026-09-29). Now setup also needs a one-time token that only
// someone with sudo on the box can read.
//
// The token lives in a file (0600, owned by the service account) next to
// auth.json. The FILE is the single source of truth: `serve` and the
// `paladin setup-token` CLI both create it if missing and otherwise read it,
// and every setup request re-reads it. So a token the CLI created after
// Paladin started is honoured without a restart. It never expires while no
// admin exists, and it is deleted once the first admin is created.

// Setup-token errors. The handler maps each to a distinct message.
var (
	ErrSetupTokenMissing = errors.New("setup token missing from request")
	ErrSetupTokenWrong   = errors.New("setup token is wrong")
	ErrNoSetupToken      = errors.New("no setup token exists on the server")
)

// EnsureSetupToken returns the token stored at path, creating it first if
// the file doesn't exist. created reports whether this call wrote the file
// (the CLI uses that to fix ownership when it runs as root).
func EnsureSetupToken(path string) (token string, created bool, err error) {
	if tok, err := readSetupToken(path); err == nil {
		return tok, false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", false, fmt.Errorf("setup token: %w", err)
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", false, fmt.Errorf("setup token: random: %w", err)
	}
	tok := hex.EncodeToString(b)
	// O_EXCL: if serve and the CLI race to create it, exactly one wins and
	// the other reads the winner's token, so both always agree.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		tok, err := readSetupToken(path)
		return tok, false, err
	}
	if err != nil {
		return "", false, fmt.Errorf("setup token: %w", err)
	}
	if _, err := f.WriteString(tok + "\n"); err != nil {
		f.Close()
		return "", false, fmt.Errorf("setup token: write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return "", false, fmt.Errorf("setup token: write %s: %w", path, err)
	}
	return tok, true, nil
}

// CheckSetupToken compares given against the stored token in constant time.
func CheckSetupToken(path, given string) error {
	if path == "" {
		return ErrNoSetupToken // not configured: fail closed
	}
	want, err := readSetupToken(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNoSetupToken
	}
	if err != nil {
		return err
	}
	given = strings.TrimSpace(given)
	if given == "" {
		return ErrSetupTokenMissing
	}
	if subtle.ConstantTimeCompare([]byte(given), []byte(want)) != 1 {
		return ErrSetupTokenWrong
	}
	return nil
}

// RemoveSetupToken deletes the token file. A missing file is fine.
func RemoveSetupToken(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("setup token: remove %s: %w", path, err)
	}
	return nil
}

func readSetupToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("setup token file %s is empty; delete it and run `sudo paladin setup-token`", path)
	}
	return tok, nil
}
