package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"

	"github.com/juicetheforce/palworld-paladin/internal/maintain"
	"github.com/juicetheforce/palworld-paladin/internal/webserv"
)

// paladinUnit is the web service's own systemd unit. scripts/install.sh
// always writes it under this name (PALADIN_UNIT); Paladin authors that
// unit itself, so this is not a guess about someone else's machine.
const paladinUnit = "paladin.service"

// cmdResetPassword implements `paladin reset-password` (run with sudo): the
// forgotten-password path. It removes the admin account, issues a fresh
// setup token and restarts Paladin, so the operator goes back through the
// first-run screen.
func cmdResetPassword(args []string) error {
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "deployment config file (default $PALADIN_CONFIG or "+defConfigPath+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := resolveConfig(*cfgPath)
	if err != nil {
		return err
	}
	return resetPassword(cfg, os.Stdin, os.Stdout, os.Stderr, restartPaladin)
}

// restartPaladin restarts the web service. The restart is the point, not a
// courtesy: `serve` holds the admin account and every login session in
// memory, so until it restarts the old password and old sessions would
// keep working.
func restartPaladin() error {
	out, err := exec.Command("systemctl", "restart", paladinUnit).CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl restart %s: %w (%s)", paladinUnit, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// resetPassword does the work. Like setup-token, stdout carries ONLY the
// token and every message goes to stderr.
func resetPassword(cfg AppConfig, in io.Reader, stdout, stderr io.Writer, restart func() error) error {
	tokenPath := cfg.setupTokenFile()
	auth, err := webserv.LoadAuthStore(cfg.authFile())
	if err != nil {
		return resetSudoHint(err)
	}

	if auth.NeedsSetup() {
		tok, created, err := webserv.EnsureSetupToken(tokenPath)
		if err != nil {
			return resetSudoHint(err)
		}
		if created && os.Geteuid() == 0 {
			if err := chownToDataDirOwner(tokenPath); err != nil {
				return err
			}
		}
		fmt.Fprintln(stderr, "No Paladin admin account exists, so there's nothing to reset. Here's the setup token:")
		fmt.Fprintln(stdout, tok)
		fmt.Fprintln(stderr, "Enter it on Paladin's first-run screen, then create your admin password.")
		return nil
	}

	// The restart below would cut a running maintenance cycle off midway.
	// An unclosed journal means one is running or was interrupted.
	u, err := maintain.ReadUnclosed(cfg.journalDir())
	if err != nil {
		return resetSudoHint(err)
	}
	if u != nil {
		return fmt.Errorf("a maintenance cycle (%s, %s) is running or was interrupted at step %s. Wait for it to finish, or run 'sudo paladin recover', then try again",
			u.CycleID, u.Kind, u.LastStep)
	}

	fmt.Fprint(stderr, "This removes the Paladin admin account and restarts the Paladin web service\n"+
		"(signing everyone out). Your server, world, settings and backups are not touched.\n"+
		"Continue? [y/N] ")
	answer, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
	default:
		fmt.Fprintln(stderr, "Cancelled. Nothing was changed.")
		return nil
	}

	if err := os.Remove(cfg.authFile()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return resetSudoHint(fmt.Errorf("remove admin account: %w", err))
	}
	tok, created, err := webserv.EnsureSetupToken(tokenPath)
	if err != nil {
		return resetSudoHint(err)
	}
	if created && os.Geteuid() == 0 {
		if err := chownToDataDirOwner(tokenPath); err != nil {
			return err
		}
	}
	restartErr := restart()

	// The token is printed whatever happened with the restart: the account
	// is already gone, and the operator needs the token either way.
	fmt.Fprintln(stdout, tok)
	if restartErr != nil {
		fmt.Fprintln(stderr, "The admin account was removed, but Paladin couldn't be restarted:", restartErr)
		fmt.Fprintln(stderr, "Run: sudo systemctl restart paladin   (until then, the old password still works)")
		return fmt.Errorf("restart failed: %w", restartErr)
	}
	fmt.Fprintln(stderr, "Done. Open the Web UI, enter this setup token, then create a new admin password.")
	return nil
}

func resetSudoHint(err error) error {
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("%w (run it with sudo: sudo paladin reset-password)", err)
	}
	return err
}
