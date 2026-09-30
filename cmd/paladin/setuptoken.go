package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/juicetheforce/palworld-paladin/internal/webserv"
)

// cmdSetupToken implements `paladin setup-token`: print the first-run setup
// token (see internal/webserv/setuptoken.go for why it exists). It's meant
// to be run with sudo, so anyone with sudo on the box can always get it.
func cmdSetupToken(args []string) error {
	fs := flag.NewFlagSet("setup-token", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "deployment config file (default $PALADIN_CONFIG or "+defConfigPath+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := resolveConfig(*cfgPath)
	if err != nil {
		return err
	}
	return setupToken(cfg, os.Stdout, os.Stderr)
}

// setupToken prints ONLY the token on stdout, and every human-facing message
// on stderr. The installer relies on that split: it captures stdout, so an
// empty stdout means "nothing to show" (setup complete, or an older binary
// that doesn't know this subcommand).
func setupToken(cfg AppConfig, stdout, stderr io.Writer) error {
	tokenPath := cfg.setupTokenFile()
	auth, err := webserv.LoadAuthStore(cfg.authFile())
	if err != nil {
		return withSudoHint(err)
	}
	if !auth.NeedsSetup() {
		// Tidy a leftover token (e.g. setup finished while its delete
		// failed). Nothing secret is printed.
		if err := webserv.RemoveSetupToken(tokenPath); err != nil {
			return withSudoHint(err)
		}
		fmt.Fprintln(stderr, "Setup is already complete: an admin account exists. No setup token is needed.")
		return nil
	}
	tok, created, err := webserv.EnsureSetupToken(tokenPath)
	if err != nil {
		return withSudoHint(err)
	}
	if created && os.Geteuid() == 0 {
		// Created by root via sudo: hand it to the service account, or
		// Paladin (which runs as that account) couldn't read it.
		if err := chownToDataDirOwner(tokenPath); err != nil {
			return err
		}
	}
	fmt.Fprintln(stdout, tok)
	fmt.Fprintln(stderr, "Enter this token on Paladin's first-run screen. It stays valid until you create your admin account.")
	return nil
}

// prepareSetupToken runs at `serve` startup: make sure a token exists while
// no admin does, and remove a leftover one once an admin exists. Failures
// are warnings, not fatal: setup then reports the problem in the UI.
func prepareSetupToken(auth *webserv.AuthStore, tokenPath string) {
	if !auth.NeedsSetup() {
		if err := webserv.RemoveSetupToken(tokenPath); err != nil {
			fmt.Fprintln(os.Stderr, "warning:", err)
		}
		return
	}
	if _, _, err := webserv.EnsureSetupToken(tokenPath); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not create the first-run setup token:", err)
	}
	// Never log the token itself: service logs are readable more widely
	// than the 0600 token file. (cmdServe prints the first-run hint.)
}

// chownToDataDirOwner gives the token file (and its directory, if root owns
// it) to whoever owns the data dir, the parent of paladin-config/. That's
// the service account; detected, not assumed.
func chownToDataDirOwner(tokenPath string) error {
	cfgDir := filepath.Dir(tokenPath)
	dataDir := filepath.Dir(cfgDir)
	fi, err := os.Stat(dataDir)
	if err != nil {
		return fmt.Errorf("setup token: %w", err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("setup token: cannot read the owner of %s", dataDir)
	}
	uid, gid := int(st.Uid), int(st.Gid)
	if di, err := os.Stat(cfgDir); err == nil {
		if dst, ok := di.Sys().(*syscall.Stat_t); ok && dst.Uid == 0 {
			if err := os.Chown(cfgDir, uid, gid); err != nil {
				return fmt.Errorf("setup token: %w", err)
			}
		}
	}
	if err := os.Chown(tokenPath, uid, gid); err != nil {
		return fmt.Errorf("setup token: %w", err)
	}
	return nil
}

func withSudoHint(err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("%w (run it with sudo: sudo paladin setup-token)", err)
	}
	return err
}
