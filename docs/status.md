# Status

Read this at the start of every task. Update it when a task changes what's
here. Items marked **VERIFY** are unconfirmed. Check them against the repo
(tags, code) or ask Ryan. Never guess.

## Version

- Latest release: **v0.3.0** (full server reset). Tags v0.1.0–v0.3.0 all
  exist locally, on origin, and as published GitHub releases, each with a
  tarball and `.sha256`.
- Live box: last confirmed on **v0.1.7**. **VERIFY:** only Ryan can say
  what it runs now (`paladin version` on the box).

## Environments

- **Laptop:** Fedora. Development, commits, and Claude Code. The only machine
  Claude Code touches.
- **palworld-test:** a Proxmox VM running Ubuntu 24.04. Integration testing
  and `scripts/release.sh` builds. Hand-built binaries report `dev`. Its
  sudoers was hand-written, not installer-generated (see learnings).
- **Live box ("Palbrary"):** Ubuntu Server. Deployed with
  `curl -fsSL https://raw.githubusercontent.com/juicetheforce/palworld-paladin/main/scripts/install.sh | sudo bash`
  (update mode). The UI is on the LAN at port 8080.

## Deploy train (reference)

1. Laptop: commit and push.
2. VM: `git pull && ./scripts/release.sh vX.Y.Z`.
3. Laptop: `scp` the `dist-release/*` artifacts, then `git tag` and
   `git push origin <tag>`.
4. GitHub: draft the release and attach both files (the tarball and its
   `.sha256`).
5. Live box: run the curl install/update one-liner.

## Shipped

- Dashboard: FPS dial, players, uptime, in-game clock, host metrics with
  steal-time detection, and a game-memory chart with the threshold line.
  (There is no separate Metrics page.)
- Settings editor: 120 keys, staged commits, gotchas, tooltips, reset to
  defaults.
- Live map: actors from the game's `/game-data`, placed with the
  palworld-coord transform, over an operator-supplied map image.
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

## In flight

- Investigating a feature request from a public GitHub comment. Design
  discussion happens in the claude.ai Project first; don't start building
  from the comment.

## Backlog / known gaps

- **The committed `dist` is ahead of `web/src`.** v0.3.0's bundle includes
  the reset confirmation-field styling (`.reset-confirm`, `.reset-word`),
  but that source never landed in `web/src` (commits `fbc4cad`/`9992bcb`). A
  rebuild from `web/src` drops it. Recover or rewrite the source before the
  next frontend change.
- Startup recovery of interrupted cycles. `serve` never reads an unclosed
  maintenance journal; only the CLI `commit`/`restore` refuse to run over
  one, and `paladin recover` reports it.
- Persist login sessions across Paladin restarts (low priority; they are in
  memory today).
- Installer update mode doesn't rewrite the sudoers grant, so grant changes
  never reach existing installs. It also doesn't check the tarball's
  `.sha256`.
- First-run `POST /api/setup` needs no login until an admin exists, so
  anyone who can reach the port can claim it in that window.
- CI doesn't run `gofmt -l` or check that `dist` matches `web/src`.
- The CLI usage text still calls itself "trial CLI" (`cmd/paladin/main.go`,
  `usage()`).
- Backups have no automatic retention; pruning is manual (web multi-select
  delete, or `paladin backup prune --keep N`).
- Map touch gestures (pan/pinch) on mobile.

## Open questions

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
