// Package update implements the server-update maintenance payload
// (DESIGN.md §6.4 Update): announce → save → stop → backup → SteamCMD
// update → start → verify, as a maintain.Payload riding the same proven
// state machine as commit and restore.
//
// The pre-check compares the installed buildid against Steam's public
// branch BEFORE anything is touched — an up-to-date server is never
// stopped. All dependencies are injected as funcs so the payload is fully
// testable without steamcmd or a live server.
package update

import (
	"context"
	"errors"
	"fmt"

	"github.com/juicetheforce/palworld-paladin/internal/maintain"
)

// ErrUpToDate aborts the cycle cleanly when no update exists. The server
// is never stopped in this case; callers should present it as a normal
// "nothing to do" result, not a failure.
var ErrUpToDate = errors.New("server is already up to date")

// Deps are the injected capabilities the payload needs.
type Deps struct {
	// LocalBuildID reads the installed buildid from the app manifest.
	LocalBuildID func() (string, error)
	// RemoteBuildID queries Steam's public-branch buildid.
	RemoteBuildID func(ctx context.Context) (string, error)
	// RunUpdate performs the SteamCMD update, streaming output lines.
	RunUpdate func(ctx context.Context, onLine func(string)) error
	// GameVersion reads the game's version string via REST /info. Called
	// while the server is up (pre-check) and again at verify.
	GameVersion func(ctx context.Context) (string, error)
	// Backup creates the pre-update world backup; returns its path.
	Backup func(ctx context.Context) (string, error)
	// OnLine receives steamcmd output lines for the live viewer. Optional.
	OnLine func(string)
}

// Payload implements maintain.Payload for a server update.
type Payload struct {
	d Deps

	// State captured across steps for reporting.
	localBefore   string
	remote        string
	versionBefore string
	backupPath    string

	// UpToDate is set during PreCheck when no update exists, so the caller
	// can distinguish "clean no-op" from a real abort after Run returns.
	UpToDate bool
}

func New(d Deps) (*Payload, error) {
	if d.LocalBuildID == nil || d.RemoteBuildID == nil || d.RunUpdate == nil {
		return nil, fmt.Errorf("update: LocalBuildID, RemoteBuildID and RunUpdate are required")
	}
	return &Payload{d: d}, nil
}

func (p *Payload) Name() string { return "update" }

// PreCheck: determine whether an update exists at all. Runs while the
// server is still up; ErrUpToDate aborts the whole cycle untouched.
func (p *Payload) PreCheck(ctx context.Context) error {
	local, err := p.d.LocalBuildID()
	if err != nil {
		return fmt.Errorf("read installed buildid: %w", err)
	}
	p.localBefore = local

	remote, err := p.d.RemoteBuildID(ctx)
	if err != nil {
		return fmt.Errorf("query Steam for latest buildid: %w", err)
	}
	p.remote = remote

	if p.d.GameVersion != nil {
		if v, err := p.d.GameVersion(ctx); err == nil {
			p.versionBefore = v
		}
		// Version is for reporting only; a failure here isn't fatal.
	}

	if local == remote {
		p.UpToDate = true
		return fmt.Errorf("%w (buildid %s)", ErrUpToDate, local)
	}
	return nil
}

// Backup: pre-update world copy. The update itself only touches game
// binaries, but the first post-update START may migrate the world format —
// this backup is the recovery anchor if that migration misbehaves.
func (p *Payload) Backup(ctx context.Context) error {
	if p.d.Backup == nil {
		return fmt.Errorf("update: backup capability not wired")
	}
	path, err := p.d.Backup(ctx)
	if err != nil {
		return err
	}
	p.backupPath = path
	return nil
}

// Apply: run the SteamCMD update, streaming output to the live viewer.
func (p *Payload) Apply(ctx context.Context) error {
	return p.d.RunUpdate(ctx, p.d.OnLine)
}

// RollbackApply: an honest no-op. Steam offers no binary downgrade, and a
// partial download is repaired by simply re-running the update. The world
// was never touched by Apply; the pre-update backup (the real recovery
// anchor) remains intact and is reported via Anchors.
func (p *Payload) RollbackApply(ctx context.Context) error {
	return nil
}

// Verify: after a healthy START, confirm the buildid on disk now matches
// what Steam advertised, and report the version change.
func (p *Payload) Verify(ctx context.Context) (maintain.VerifyResult, error) {
	var res maintain.VerifyResult

	localAfter, err := p.d.LocalBuildID()
	if err != nil {
		res.Warnings = append(res.Warnings,
			"could not re-read installed buildid after update: "+err.Error())
	} else if localAfter != p.remote {
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"installed buildid %s does not match Steam's advertised %s — the update may not have fully applied",
			localAfter, p.remote))
	} else {
		res.Notes = append(res.Notes, fmt.Sprintf("buildid %s → %s", p.localBefore, localAfter))
	}

	if p.d.GameVersion != nil {
		if vAfter, err := p.d.GameVersion(ctx); err == nil {
			switch {
			case p.versionBefore != "" && vAfter != p.versionBefore:
				res.Notes = append(res.Notes, fmt.Sprintf("game version %s → %s", p.versionBefore, vAfter))
			case p.versionBefore != "" && vAfter == p.versionBefore:
				res.Warnings = append(res.Warnings,
					"game version unchanged ("+vAfter+") despite a buildid change — worth a manual look")
			default:
				res.Notes = append(res.Notes, "game version now "+vAfter)
			}
		} else {
			res.Warnings = append(res.Warnings, "could not read game version after restart: "+err.Error())
		}
	}

	if p.backupPath != "" {
		res.Notes = append(res.Notes, "pre-update world backup at "+p.backupPath)
	}
	return res, nil
}

func (p *Payload) Anchors() []string {
	if p.backupPath == "" {
		return nil
	}
	return []string{p.backupPath}
}
