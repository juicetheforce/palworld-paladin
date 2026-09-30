# Decisions

This file records why things are the way they are. Add an entry when a task
makes a new decision. Mark superseded entries rather than deleting them.

This file, the code and `docs/status.md` are the current record.
`docs/DESIGN.md` is the original design (frozen at rev 17); where it
disagrees with them, it is out of date.

**Go single static binary with `go:embed`.** No runtime and no dependency
resolution on target hosts. The frontend bundle and the settings key list are
both embedded. The stdlib comes first: auth hashing is stdlib PBKDF2 rather
than bcrypt, to keep `go.mod` clean.

**`sav_cli` as a pinned sidecar process.** PST's save parser (via `sav_cli`)
is used through a process boundary, never linked or vendored. Its Oodle
decompression dependency carries GPL-3.0 implications, and the boundary keeps
Paladin's Apache-2.0 licensing clean. It is pinned at PST v0.12.2 and fetched
by the installer with dual checksums (archive and binary).

**Model A identity.** One unprivileged service account runs both Paladin and
the game server. The web login is app-level, hashed, and decoupled from OS
users.

**A scoped sudoers grant, not polkit.** *(Corrected 2026-09-29: the earlier
entry gave the verbs as a shorthand; this is the exact grant.)*
`scripts/install.sh` (`write_sudoers`) writes `/etc/sudoers.d/paladin` with
exactly these lines, where `<user>` is the service account and `<unit>` is
`palserver.service`:

```
<user> ALL=(root) NOPASSWD: /usr/bin/systemctl start <unit>
<user> ALL=(root) NOPASSWD: /usr/bin/systemctl stop <unit>
<user> ALL=(root) NOPASSWD: /usr/bin/systemctl restart <unit>
<user> ALL=(root) NOPASSWD: /usr/bin/systemctl kill <unit>
<user> ALL=(root) NOPASSWD: /usr/bin/systemctl show <unit>
<user> ALL=(root) NOPASSWD: /usr/bin/systemctl is-active <unit>
<user> ALL=(root) NOPASSWD: /usr/bin/systemctl status <unit>
<user> ALL=(root) NOPASSWD: /usr/bin/systemctl kill -s SIGKILL <unit>
```

Paladin itself only calls `start`, `stop`, `restart`, `kill -s SIGKILL` and
`show` (`internal/supervise/unit.go`), each through `sudo -n`. Bare `kill`,
`is-active` and `status` are granted but unused. There is no broad sudo,
ever.

**Paladin is the sole supervisor.** It owns both systemd units
(`palserver.service` and Paladin's own). *(Corrected 2026-09-29: an earlier
entry said adoption was read-only until an explicit takeover.)* Detection is
read-only: `install.sh --check` changes nothing. Adopting asks once, then
replaces the server's existing launcher with Paladin's unit. Rival
supervisors are detected from an anchored known-list, and each one is
stopped and disabled only after its own consent prompt. Docker-run servers
are declined, because compose and env-driven config would clobber ini edits.

**Transactional commit-and-restart.** The sequence is announce → save → stop
→ backup → apply → start → verify, with rollback anchors (the pre-stop backup
and the pre-apply ini copy). Settings are never presented as "live."
Verification reads back effective values, and gotchas are surfaced.

**Web-triggered cycles auto force-kill on stop timeout.** If the server
misses the stop grace window (90 s) during a cycle started from the web UI,
`serve`'s `StopDecider` always chooses force-kill (`cmd/paladin/main.go`).
This has been deliberate since the v0.1.2 live incident: the grace window was
missed on a real world, nobody could answer a dialog in a headless cycle, the
cycle cancelled, and the server was left down mid-maintenance. The world was
saved at the SAVE step seconds earlier, so the kill loses nothing. Only the
CLI subcommands prompt the operator (force kill or cancel). The engine itself
never kills on its own: a nil decider means cancel.

**Cycles tolerate a stopped server.** Commit, restore and reset run with
`TolerateStopped` (`internal/maintain/engine.go`). If the pre-check health
probe fails and the unit is genuinely inactive, the cycle runs offline: it
skips announce/save/stop (recorded as skips) and continues at backup. A
server that is down is exactly when a restore is most needed. A unit that is
*active* while its API doesn't respond (a wedged server) still aborts,
because touching world files under a live process is never safe.

**One optional broadcast, not a countdown.** Before a cycle, the operator can
give one broadcast message and a delay. If the message is empty, nothing is
announced. There is no 5/2/1-minute countdown.

**REST only; no RCON fallback.** *(Corrected 2026-09-29: an earlier entry
said "REST primary, RCON fallback". No RCON client was ever built.)*
`internal/palapi` speaks REST only. Pocketpair deprecated RCON, and fresh
installs write `RCONEnabled=False`. REST uses Basic auth over plain HTTP, so
Paladin talks to it on `127.0.0.1` only.

**Settings as data.** `data/palworld-settings.json` is the single source for
the form, the validation, the tooltips, and the gotchas. Game patches become
data edits, and community PRs can maintain it.

**Ban list.** Ban and unban go through REST. The list is read from
`banlist.txt`, because REST doesn't expose it. The file format is
`steam_<id>,<hex>` per line. The list stays readable while the server is
stopped.

**The built frontend is committed.** `internal/webserv/dist` is committed, so
release builds and deploys need no Node toolchain.

**Onboarding is done by the installer.** Detection, fresh install, adopt and
takeover all live in `scripts/install.sh`. There is no web onboarding wizard.
The web UI's only onboarding step is the first-run screen that sets the admin
password (`POST /api/setup`, accepted only while no admin exists, and only
with the setup token; see below).

**First-run setup token.** Because the UI listens on all interfaces, first
run used to let whoever reached the port first claim the admin account
(audit 2026-09-29). Now setup needs a one-time token that anyone with sudo on
the box can get, and nobody else can:
- **Where:** `<data_dir>/paladin-config/setup-token`, next to `auth.json`
  (fresh installs: `/home/palworld/paladin-config/setup-token`). Mode 0600,
  owned by the service account. 128 bits from `crypto/rand`, as 32 hex chars.
- **The file is the source of truth.** `serve` creates it at startup while no
  admin exists; `paladin setup-token` creates it if missing. Creation uses
  `O_EXCL`, so if both race, the loser reads the winner's token. Every setup
  request re-reads the file, so a token created after Paladin started works
  without a restart.
- **Lifetime:** it never expires while no admin exists, and it's deleted when
  the first admin is created (and tidied by `serve` or the CLI if one is
  left over).
- **Check:** constant-time compare after trimming whitespace. Missing and
  wrong tokens get 401 with distinct messages; no token file gets 503. All
  three say to run `sudo paladin setup-token`. The existing 409 "already
  configured" check runs first, so installs with an admin see no change.
- **`paladin setup-token`** (run with sudo): with no admin, prints the token.
  With an admin, says setup is complete and prints nothing secret. Its
  stdout is the token and nothing else, and every human message goes to
  stderr, because the installer captures stdout. When run as root and it
  creates the file, it chowns it to the owner of `data_dir` (detected, not
  assumed) so the service account can read it.
- **Installer:** prints the token next to the Web UI URL whenever no admin
  exists: the fresh/adopt summary, plus every update-mode rerun (up to date,
  aborted, or updated). `--check` stays read-only and never creates one. An
  older binary without the subcommand yields empty stdout, so the installer
  prints nothing instead of failing.
- **Not built:** a password-reset command. Today a forgotten password means
  deleting `auth.json` and restarting Paladin, which reopens first-run setup
  with a new token (documented in the README).

**Installer model.**
- One curl-pipe command both installs and updates.
- Update mode swaps the binary and restarts Paladin without touching the game
  server.
- `--check` mode is read-only.
- Fresh install supports apt and dnf. Adopt works on any x86_64 systemd
  distro.
- `uninstall.sh` keeps the game server by default. Deleting world data sits
  behind a type-DELETE gate.

**Testing on VMs goes through the real installer.** `scripts/deploy-test.sh
<host> [--fresh|--update]` builds on the laptop with the same packaging code
as `release.sh` (`scripts/lib/build.sh`), then runs `install.sh
--local-archive` on a disposable VM, so fresh-install and update paths are
tested exactly as users run them. Guards and conventions:
- It refuses any host not listed in the gitignored `scripts/test-hosts`.
- Builds are stamped `dev-<commit>[-dirty]`, and count as dev builds
  everywhere: no update indicator, and the installer never considers them
  "already up to date".
- `install.sh --local-archive` verifies the tarball against a `.sha256`
  beside it (comparing hashes directly, not via `sha256sum -c`), before
  anything on the machine changes.
- Claude runs it only when a prompt asks.
- See `docs/testing.md`.

**Update indicator.** A lazy GitHub check, cached for 12 hours and run only
while the UI is being polled. It is suppressed on `dev` builds and links to
`releases/latest`.

**Auth: a JSON users list, in-memory sessions.** *(Corrected 2026-09-29: an
earlier entry said "users table". There is no database.)* Credentials live in
`auth.json` (mode 0600), which holds a list of users, each with a role.
Passwords are hashed with stdlib PBKDF2-HMAC-SHA256
(`internal/webserv/passwordhash.go`). There is one admin today, but the list
shape means RBAC can be added later without a rewrite. Sessions are held in
memory with a 12-hour TTL, so restarting Paladin logs everyone out.

**Map: live `/game-data` over a map image.** The live map plots actors from
the game's `/game-data` endpoint, which the installer's server unit enables
with `-enable-gamedata-api`. Coordinates use the palworld-coord transform.
*(Corrected 2026-09-29: an earlier version of this entry said Paladin bundles
no map artwork. It does.)* The underlay is an operator-supplied image at
`<data_dir>/paladin-config/worldmap.png` if present, served at
`/api/map-image`; otherwise it's the bundled `worldmap.jpg` (© Pocketpair,
stitched from PST's tiles, credited in the README).

**No Metrics page.** Host and game metrics live on the Dashboard. There is
no separate Metrics page and no server-side metrics history.

**The UI listens on all interfaces.** The installer writes
`"listen": "0.0.0.0:<port>"` (default 8080), deliberately, so phones and
other LAN devices can reach it. The operator is responsible for keeping that
port off the WAN. The binary's own default, used only without a config, is
`127.0.0.1:8080`. The game's REST API is used on localhost only, and RCON is
disabled on fresh installs.

**Out of scope.** Multiple instances, Windows, Docker servers, editing
`WorldOption.sav` (link out instead), and RBAC (see Auth above).

**Versioning and licence.** *(Corrected 2026-09-29: an earlier entry said
versions stay v0.1.x, but releases have moved on; the latest tag is
v0.3.0.)* Versions stay 0.x and modest. Only Ryan decides a minor or major
bump. Licensed Apache-2.0.
