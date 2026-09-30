# Learnings

These are lessons that were expensive to learn. Most surfaced only on real
hardware, never in a sandbox or on the test VM. Each entry gives the symptom,
the cause, and the rule it left behind. When a new production bug is fixed,
add it here.

## Permissions and supervision

**sudoers exact-match #1: force-kill denied.**
- Symptom: a world outlived the stop grace window, and the escalation to
  `systemctl kill -s SIGKILL <unit>` was denied.
- Cause: the grant had a bare `kill <unit>` line, and sudoers matches
  arguments exactly.
- Fix: `install.sh` grants the exact `kill -s SIGKILL <unit>` invocation.

**sudoers exact-match #2: stopped servers "timed out" (v0.1.4, live box).**
- Symptom: the server stopped within seconds, but the stop-wait ran to its
  90-second timeout, and the memory guard read nothing.
- Cause: `Show()` used `show <unit> --property=...`, which the grant doesn't
  cover. Every call was silently denied.
- Fix: `Show()` calls bare `show <unit>` and parses the full output. A test
  pins the exact argument list.
- Rule: never add a flag to a sudo'd `systemctl` call without changing the
  grant in the same commit.

**The test box is not representative for permissions.**
- `palworld-test`'s sudoers was hand-written early on; the installer never
  generated it. That is why the `--property` bug never appeared there.
- Rule: verify permission and installer changes on an installer-provisioned
  box.

## Installer (bash)

**SIGPIPE password generator.**
- Symptom: the installer died silently.
- Cause: under `set -euo pipefail`, `tr -dc ... < /dev/urandom | head -c 20`
  dies of SIGPIPE.
- Fix: `gen_pw` reads a finite chunk first (`head -c 512 /dev/urandom |
  base64 | tr ... | head -c 20`).
- The installer has an ERR trap so failures print their line number and are
  never silent. Keep it.

**pgrep 15-character truncation.**
- Cause: `pgrep -x` matches against the kernel's 15-character process name,
  so `PalServer-Linux-Shipping` never matches.
- Rule: use full-command-line matching (`-f`). There is a comment in the
  code; don't "fix" it back.

**pstore false positive in rival detection.**
- Symptom: rival detection flagged the host's unrelated pstore services as a
  competing supervisor.
- Cause: a substring grep.
- Fix: an anchored list of known supervisors, matched as unit-name prefixes
  (`pst`, `palpanel`, `palworld-server-tool`, `palworld-admin`,
  `palworld-server-manager`). The server's own unit is excluded from the
  list.

**Found adopting the live box: world root path, steamapps manifest layout.**
- Real installs didn't match the assumed layout.
- Rule: detect, don't assume. The specifics are in the regression tests and
  git history; expand this entry the next time you touch that code.

## Settings / ini

**CrossplayPlatforms comma-split corruption.**
- palworld-admin corrupted the live ini by splitting `OptionSettings` on
  commas, breaking the `CrossplayPlatforms=(...)` tuple.
- Paladin's parser is tuple-aware for this reason. Never regress it.

**Ini escape parsing (live box).**
- Quoted values with escapes were mis-parsed.
- The specifics are pinned in the settings regression tests; expand this
  entry the next time you touch that code.

**A stray newline inside `OptionSettings=(...)` silently reverts the server
to defaults.** Writes must keep the line whole, and must verify it after
writing.

## UI

**Swallowed UI errors (live box).** Failed API calls looked like nothing had
happened. Every failure must reach the user.

## Map

**Player dot in the ocean.**
- The coordinate transform was correct: it reproduces palworld-coord's
  worked example. The placement onto the map image was wrong.
- It was fixed from data: the in-game coordinate readout, compared with
  Paladin's tooltip, compared with where the player actually stood.
- Rule: fix map issues from in-game data points, not guess-and-rebuild
  loops.

## General

- **Real hardware finds what tests don't.** A feature isn't done until it has
  been verified against a real server. Say plainly what was verified and
  what wasn't.
- **`dev` is a correct version.** A plain `go build` reports `dev`;
  `scripts/deploy-test.sh` builds report `dev-<commit>[-dirty]`. Both are
  dev builds (no update indicator; the installer never treats them as up to
  date). If a test VM ever shows a release version number, something is
  wrong.
