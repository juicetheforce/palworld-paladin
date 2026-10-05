# Status

Read this at the start of every task. Update it when a task changes what's
here. Items marked **VERIFY** are unconfirmed. Check them against the repo
(tags, code) or ask Ryan. Never guess.

## Version

- Latest release: **v0.3.1** (2026-10-05; first-login setup token,
  password management, honest installer summary). Published from the
  laptop via `/release`, and verified from the public API (tag, both assets,
  downloaded tarball matches its `.sha256`). Tags v0.1.0–v0.3.1 all exist
  locally, on origin, and as published GitHub releases, each with a tarball
  and `.sha256`.
- Live box: last confirmed on **v0.1.7**. **VERIFY:** only Ryan can say
  what it runs now (`paladin version` on the box).

## Environments

- **Laptop:** Fedora. Development, commits, and Claude Code. The only machine
  Claude Code touches.
- **palworld-test:** a Proxmox VM running Ubuntu 24.04, used for
  integration testing. Hand-built binaries report `dev`. Its sudoers was
  hand-written, not installer-generated (see learnings). Releases are no
  longer built here.
- **Test VMs:** disposable Proxmox VMs restored from snapshots, for
  fresh-install and update tests (`docs/testing.md`).
- **Live box ("Palbrary"):** Ubuntu Server. Deployed with
  `curl -fsSL https://raw.githubusercontent.com/juicetheforce/palworld-paladin/main/scripts/install.sh | sudo bash`
  (update mode). The UI is on the LAN at port 8080.

## Testing on VMs

The primary test is the published release, installed with the public curl
one-liner on a fresh VM (`docs/testing.md`). `scripts/deploy-test.sh` covers
unreleased builds. **v0.3.1's first-login checklist has not been run on real
hardware yet**; it stays not verified until Ryan runs it.

## Release train (reference)

1. Ryan asks for a release; Claude Code runs `/release vX.Y.Z` on the
   laptop. It checks, builds, tags, publishes the GitHub release, and
   verifies it from the public API (tag, both assets, checksum of the
   downloaded tarball).
2. Ryan: fresh-VM test with the public curl one-liner (`docs/testing.md`).
3. Ryan: update the live box with the same one-liner.

## Shipped

- Dashboard: FPS dial, players, uptime, in-game clock, host metrics with
  steal-time detection, and a game-memory chart with the threshold line.
  (There is no separate Metrics page.)
- Settings editor: 120 keys, staged commits, gotchas, tooltips, reset to
  defaults.
- Live map: actors from the game's `/game-data`, placed with the
  palworld-coord transform, over the bundled map image (or an
  operator-supplied one).
- Two-tier Players page with kick, ban, unban, and the ban list, plus a
  guilds table.
- Backups with offline-tolerant restore and multi-select delete.
- Full server reset (v0.3.0): forced pre-reset backup, typed confirmation,
  optional settings reset and ban-list clear.
- Memory-threshold auto-restart (operator-configured; off until set).
- One-button server update.
- JSONL event log with rotation, and an SSE activity feed.
- Broadcast, force-save, and restart.
- Mobile layout with a drawer nav.
- Installer (fresh, adopt, takeover, `--check`, update) and uninstaller.
- **v0.3.1:**
  - two-step first run (setup token → create password);
  - first-run setup token (`sudo paladin setup-token`, printed by the
    installer);
  - change password in the UI;
  - `sudo paladin reset-password`;
  - installer summary messaging that matches what actually happened.

## In flight

- Investigating a feature request from a public GitHub comment. Design
  discussion happens in the claude.ai Project first; don't start building
  from the comment.

## Backlog / known gaps

- Startup recovery of interrupted cycles. `serve` never reads an unclosed
  maintenance journal; only the CLI `commit`/`restore` refuse to run over
  one, and `paladin recover` reports it.
- Persist login sessions across Paladin restarts (low priority; they are in
  memory today).
- Installer update mode doesn't rewrite the sudoers grant, so grant changes
  never reach existing installs. It also doesn't check the tarball's
  `.sha256`.
- CI doesn't run `gofmt -l` or check that `dist` matches `web/src`.
- Backups have no automatic retention; pruning is manual (web multi-select
  delete, or `paladin backup prune --keep N`).
- Map touch gestures (pan/pinch) on mobile.

## Open questions

- `sudo paladin setup-token` fails with "command not found" on RHEL-family
  distros: their default sudo `secure_path` is `/sbin:/bin:/usr/sbin:/usr/bin`
  (checked in the AlmaLinux 8/9/10 sudo packages), and the binary lives in
  `/usr/local/bin`. Fedora 44 and Ubuntu 24.04 include `/usr/local/bin`, so
  they're fine. The README already gives the full-path fallback. Fix
  proposed (full path as the primary command); awaiting Ryan.
- Should the server unit pass `-log`? DESIGN rev 17 says both flags were
  added to the unit, but the installer's unit passes only
  `-enable-gamedata-api` (`scripts/install.sh`, `write_server_unit`). Rev 17
  also found that the Linux build writes no Pal.log even with `-log`.
- Which community wiki to use for the settings `kb_link` values. Today 1 of
  120 keys has one (`DenyTechnologyList` → docs.palworldgame.com).
- Carried over from DESIGN §11, still open:
  - Exactly which keys `WorldOption.sav` overrides on a 1.0 dedicated
    server. Commits already detect it and clear it for world keys.
  - Whether hot backups are consistent on a large (multi-GB) world.
  - Backup retention defaults (see backlog).
  - A default memory-restart threshold. Today it's off until the operator
    sets one (0.5–512 GB).

## Settled (formerly open in DESIGN §11 or here)

- Per-player messaging (whisper): not supported by the REST API; only
  `/announce` exists (`internal/palapi/endpoints.go`).
- Save-parser integration: `sav_cli` as a pinned sidecar process (PST
  v0.12.2), never linked.
- Scoped-grant mechanism: sudoers (see decisions.md for the exact grant).
- Maintenance timeouts: pre-check 15 s, save 60 s, stop grace 90 s, kill
  grace 15 s, start 300 s (`internal/maintain/types.go`).
- Backup integrity check: a manifest of files and sizes; checksums remain a
  possible future deep-verify (`internal/backup/backup.go`).
- Adopt reuses the detected service account (`SVC_USER="$SRV_USER"` in
  `install.sh`).
- `/game-data` works on v1.0.1 when the server runs with
  `-enable-gamedata-api`, which the installer's unit sets.
- STOP escalation: web cycles auto force-kill; the CLI asks (see
  decisions.md).
