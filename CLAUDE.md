# Paladin

Self-hosted web admin panel for a single Palworld dedicated server on Linux.
The Go backend ships as a single static binary with the React/TypeScript
frontend embedded via `go:embed`. Licensed Apache-2.0. Repo:
`github.com/juicetheforce/palworld-paladin`. Latest release: **v0.3.0**
(check `git tag` for newer).

Paladin exists to close two gaps that existing tools leave open:

1. **A settings list that stays current.** Keys live in a data file, so
   supporting a game patch is a data edit.
2. **Honesty about "applied."** Settings changes go through a transactional
   commit-and-restart that verifies the result, and known traps get warnings.

Any change that weakens either of these is a regression. Flag it.

## How we work (read this first)

- **Tasks arrive as prompts.** Ryan does design discussion in a separate
  claude.ai Project and brings tasks here as prompts. Stay inside each task's
  scope. If you notice something else worth doing, list it in the wrap-up;
  don't do it.
- **Questions are not approvals.** "Could we…", "would it be possible…", or
  "what do you think about…" asks for an answer or a proposal. Build only
  after an explicit go-ahead.
- **Diagnostics need evidence.** Never answer your own diagnostic question.
  Say what you'd check and why, then get the real output (logs, `journalctl`,
  file contents, test output) before concluding. If the evidence lives on a
  server you can't reach, ask Ryan for it and give him the exact command to
  run.
- **Keep versions and timelines modest.** Versions stay 0.x; only Ryan
  decides a minor or major bump. Don't dramatize timelines or inflate
  scope.
- **Write for the maintainer.** Ryan maintains this long-term and isn't a
  full-time developer. Prefer boring, readable code over clever code. Comment
  the *why* on anything that exists because of a real-world failure, so nobody
  "fixes" it back.
- **Pin every bug with a test.** Every bug fix gets a regression test that
  pins the actual failure, with a comment naming the incident.
- **You commit and push.** When a task's checks pass (see "Finishing a
  task"), commit with a clear message and push to `main`. Never force-push,
  rewrite pushed history, or push with failing checks. Never create tags or
  GitHub releases; `/release` prepares those for Ryan. If a prompt says not to
  commit, don't.
- **Stay on the laptop.** You run on Ryan's Fedora laptop and never touch
  `palworld-test` or the live server. When something must run there, give
  Ryan a complete, copy-paste command block with host-by-host steps in order.
  Those blocks cover only the VM and live-box steps; the laptop side is yours.

## Layout

```
cmd/paladin/          entrypoint: config, wiring, `serve` (production) and
                      the maintenance CLI subcommands
internal/
  palapi/             Palworld REST client (REST only, no RCON), banlist.txt reader
  supervise/          systemd unit control via sudo, readiness, crash/RAM restart
  maintain/           shared maintenance engine + journal; commit, restore,
                      reset and update all run as payloads on it
  settings/           ini parse/serialize, key-list loader, validation,
                      commit payload, settings reset
  backup/             backup create/catalog/prune/restore, full-reset payload
  update/             server-update payload (SteamCMD)
  steam/              SteamCMD helpers: installed vs public buildid
  roster/             player join/leave events from REST roster diffs
  sav/                sav_cli sidecar integration (Level.sav → JSON)
  events/             in-process pub/sub, JSONL event log, SSE feed
  hostmetrics/        host CPU/RAM/temps/network from /proc and /sys
  webserv/            Paladin's HTTP API, auth (auth.json, PBKDF2, in-memory
                      sessions), SSE, map, embedded frontend
    dist/             BUILT frontend bundle — committed (see web/CLAUDE.md)
data/palworld-settings.json   settings key list (embedded via data/data.go)
web/                  React + TypeScript + Vite source
scripts/              install.sh (detection, fresh install, adopt, takeover,
                      update, sudoers grant), uninstall.sh, release.sh
docs/                 see "Context docs" below
.github/workflows/    CI: go vet, build, test, settings JSON check
```

If this tree drifts from reality, update it.

## Commands

```bash
go build ./... && go vet ./... && go test ./...
gofmt -l .                         # must print nothing
cd web && npm ci && npm run build  # rebuilds internal/webserv/dist
go build -o paladin ./cmd/paladin  # local binary; reports version "dev"
bash -n scripts/install.sh scripts/uninstall.sh   # syntax check
```

Release builds use `scripts/release.sh vX.Y.Z`. Ryan runs these; use
`/release` to prepare one.

## Hard rules

- **`sav_cli` stays a separate process.** It is pinned at PST v0.12.2, with
  archive and binary checksums in `install.sh`. Never link, vendor, or import
  PST's save/Oodle code into the Paladin binary: that brings GPL-3.0 exposure.
  Changing the pin needs new checksums and Ryan's approval.
- **Respect licenses.**
  - uitok/PalPanel is GPL-3.0: read it for ideas, never copy its code.
  - palworld-admin is proprietary: use it as a behaviour reference only.
  - PST (Apache-2.0), palworld-coord (MIT), RNZ01 and amantu-qbit (MIT) are
    reusable with attribution.
  - Map artwork is © Pocketpair.
- **sudoers matches arguments exactly.** Every `systemctl` call made through
  sudo must match a line in the installer's sudoers grant exactly. Adding a
  flag (`--property`, `-s SIGKILL`, …) causes a *silent denial* on every real
  install. If you change an invocation, change the grant in `install.sh` in
  the same commit, and consider how existing installs get the new grant. See
  learnings.
- **Treat the ini parser with care.** `PalWorldSettings.ini` holds
  `OptionSettings=(...)` on a single line.
  - Never split it naively on commas: values include tuples
    (`CrossplayPlatforms=(...)`) and quoted strings with escapes.
  - A newline inside the parens silently resets the server to defaults.
  - Parser changes need round-trip tests against real-world lines.
- **Settings keys are data.** They live in `data/palworld-settings.json`.
  Adding a key is a data edit with a tooltip, plus a gotcha where one
  applies.
- **Never report success you didn't verify.** APIs and UI report what
  actually happened. Surface errors to the user; never swallow them.
- **Ask before adding dependencies.** Keep `go.mod` minimal (stdlib-first).
  Don't add a Go module or npm package without asking.
- **Keep safe defaults.**
  - The installer makes the UI listen on all interfaces (`0.0.0.0:8080`) so
    LAN devices can reach it. That is deliberate; the operator keeps the port
    off the WAN. Don't change the bind without Ryan's say-so.
  - The game's REST API is only used via localhost, and its credentials
    never leave the box. RCON is off on fresh installs.
  - Docker-run servers are declined, not adopted.
- **Detect, don't assume.** Read paths, users, and unit names from the
  machine; never hardcode them. Real installs differ from the test box.

## Context docs

@docs/learnings.md

- `docs/status.md`: current version, environments, in-flight work, backlog.
  Read it at the start of every task, and update it when a task changes it.
- `docs/decisions.md`: why things are the way they are. Read it before
  touching architecture, the installer, supervision, or the settings
  pipeline. Add an entry when a task makes a new decision.
- `docs/DESIGN.md`: the original design document, frozen at rev 17 as a
  historical record. Don't edit its body. Where it conflicts with the code,
  `decisions.md` or `status.md`, those win.

## Finishing a task

Run go vet, the tests, and the gofmt check. Also run the frontend build if
`web/` changed, and `bash -n` if a script changed. Update `docs/status.md` if
needed, then run `/wrap`, which commits and pushes.
