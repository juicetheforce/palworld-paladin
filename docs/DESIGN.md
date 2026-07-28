# Paladin — Project Scope & Design

> Paladin: an open-source, self-hosted admin panel for a single Palworld
> dedicated server. (Working name; repo/module namespace likely `palworld-paladin`.)

> Working scope document. This captures the decisions and open questions
> established during initial planning. It is meant to be the seed document
> for a dedicated project workspace.
>
> **Revision 2 — 2026-07-27.** Updated against the Palworld 1.0 release
> (2026-07-10) and the official 1.0 REST API docs. Resolves three formerly
> [open] items (ban mechanism, whisper, settings readback), adds the
> `/game-data` live-snapshot finding and the resulting two-tier visibility
> design, and adds Backup & Restore (§6.8) as an explicit first-class module.
>
> **Revision 3 — 2026-07-27.** Fully specifies the shared maintenance state
> machine: invariants and complete rollback matrix for commit *and* restore
> (§6.9), resolving the former [open] rollback item. Repo home confirmed:
> `github.com/juicetheforce/palworld-paladin`.
>
> **Revision 4 — 2026-07-27.** STOP escalation decided: force kill is
> user-initiated only, via a two-option dialog (force kill and continue, or
> cancel the job entirely) with plain-language context; never automatic.
>
> **Revision 5 — 2026-07-27.** Build-order step 0 delivered: the settings
> key-list data artifact (`palworld-settings.json`) is seeded — 119 keys
> against 1.0 (1.100.427), 16 new-in-1.0, 49 gotchas encoded, 16 entries
> flagged for test-box verification. `WorldOption.sav` override upgraded
> from unverified assertion to community consensus (§11).
>
> **Revision 6 — 2026-07-27.** Repository structure decided (§5.4):
> Go `internal/`-only layout, open questions contained behind interfaces,
> single binary via `go:embed` of the key-list data file and web bundle.
>
> **Revision 7 — 2026-07-27.** License resolved: Apache 2.0, committed to
> the repo (§4, §11). Repo is live and public at
> `github.com/juicetheforce/palworld-paladin`.
>
> **Revision 8 — 2026-07-27.** Behavioral test-box findings folded in:
> offline ban CONFIRMED with banlist.txt format captured (§6.7);
> ini-rewrite-on-shutdown claim TESTED AND NOT REPRODUCED on v1.0.1;
> `/v1/api/game-data` found DOCUMENTED BUT ABSENT (404) on the live
> v1.0.1.100619 build — live map reverts to sav-parsing until it ships
> (§5.1, §6.5); hot-backup method validated on a small world, verdict at
> scale still open (§11).
>
> **Revision 9 — 2026-07-27.** "Temperatures" resolved: host hardware
> temps, folded into a graceful-degrading host-metrics component (§6.5,
> §11).
>
> **Revision 10 — 2026-07-27.** Host-metrics page fully specified (§6.5):
> memory with threshold-annotated server RSS, hottest-core + steal-time
> CPU view, pre-flight-annotated disk with per-owner breakdown, temps,
> game-side metrics on the same page, history via the downsampled store.
>
> **Revision 11 — 2026-07-27.** Network scoped in as an observational
> panel (throughput + player-count correlation, no saturation verdicts —
> host counters can't see the upstream bottleneck) (§6.5).
>
> **Revision 12 — 2026-07-27.** Settings-readback quirks verified and
> encoded (§6.3): passwords are readback-invisible (`rest_readback:false`
> in the key list), and readback key matching must be case-insensitive
> (`AutoSaveSpan` → `autoSaveSpan`). palapi shipped and green against the
> live server; /metrics shape captured (incl. undocumented basecampnum).
>
> **Revision 13 — 2026-07-28.** First live commit AND restore succeeded
> against the test box. New guiding principle: Paladin is a **removable
> guest** (§2) — all its files under one root outside the game tree, a
> clean `uninstall`, safety copies relocated out of the save tree after a
> restore (§6.9). Fixes the world-detector collision found live (safety
> copy sibling mistaken for a second world).

---

## How to read this document

Every non-trivial claim is tagged with its confidence basis, so design
decisions are never confused with assumptions:

- **[decided]** — a deliberate design decision that has been made and agreed.
- **[confirmed]** — a factual claim verified against an external source.
- **[inference]** — reasoning from confirmed facts or sound engineering
  practice; plausible but not independently verified.
- **[open]** — an unresolved question or an unverified assumption that must
  be settled before the relevant module is built. Do not treat these as
  settled during implementation.

When implementing, if you find yourself relying on an **[open]** item as
though it were **[decided]** or **[confirmed]**, stop and resolve it first.

---

## 1. Purpose & vision

**Paladin** is a standalone Linux service that **deploys, owns, supervises,
configures, and observes exactly one Palworld dedicated server** — from a
bare host to a running, managed game server — behind a single modern web
interface. (The name: a guardian/protector for your server, and a pun on
*Pal*world admin.)

It is **not** a fork or merger of existing tools. It is a ground-up project
that learns from prior art (palworld-admin, PST) but shares no code with the
proprietary one and owns its entire control surface. **[decided]**

The premise that makes it worth building: both existing tools have the same
two blind spots — a settings list that goes stale and doesn't expose current
keys, and no honest handling of the "I changed a setting and nothing
happened" failure mode. This tool's reason to exist is to close those gaps
with a maintainable settings layer and a transactional commit-and-restart
workflow. **[decided]**

---

## 2. Guiding principles

- **Greenfield, single-purpose.** One tool, one server, one host. No
  coexistence with other managers — this tool is the sole supervisor of the
  server it manages. **[decided]**
- **Detect, don't assume.** Where the environment is unknowable in advance
  (service account name, install paths, current supervisor), the tool reads
  reality rather than hardcoding expectations. **[decided]**
- **Transactional, not live.** Palworld only loads settings at server boot,
  so settings changes are staged and applied as one atomic
  commit-and-restart unit rather than pretended to be live. **[decided]**
- **Honest about traps.** The UI surfaces known gotchas (e.g. base-cap is
  level-gated) rather than reporting "applied" and letting the user draw
  the wrong conclusion. **[decided]**
- **Maintainable by data, not by rebuild.** The settings key-list and its
  documentation live in a versioned data file, so keeping current with
  Palworld patches is a data edit (and a candidate for community PRs), not a
  code change and release cycle. **[decided]**
- **Safe by default.** LAN-only binding by default; remote access is a
  deliberate act the operator takes (e.g. Tailscale), not an accident.
  **[decided]**
- **A removable guest.** Paladin keeps everything it owns (backups, journal,
  database, logs, relocated safety copies) under a SINGLE root directory
  OUTSIDE the Steam/Palworld file tree, and ships a clean `uninstall` that
  removes Paladin without breaking the server. The only game-tree file
  Paladin ever modifies is `PalWorldSettings.ini` (the operator's own file,
  always left valid); the world folder is touched only transiently during a
  restore and always left healthy. An operator must be able to get rid of
  Paladin at any time and keep a working server. **[decided at revision 13]**

---

## 3. Scope

### In scope (v1)

- Single Palworld dedicated server, Linux host (bare metal or VM),
  **Ubuntu-first**. **[decided]**
- Two onboarding paths: **new install** and **adopt existing**. **[decided]**
- Full `PalWorldSettings.ini` editing with a current, complete v1.0 key list.
  **[decided]**
- Transactional commit-and-restart workflow (broadcast -> save -> stop ->
  backup -> apply -> start -> verify). **[decided]**
- Server lifecycle control: start, stop, restart, broadcast, update.
  **[decided]**
- **Live player administration**: roster, offline player info, kick, ban,
  unban (see §6.7). **[decided]** (Direct message / whisper is cut from v1 —
  resolved: no REST endpoint for it exists. See §6.7.)
- Read-only visibility in two tiers: a **live tier** from the REST
  `/game-data` actor snapshot (players, Pals, bases, positions, live map)
  and a **historical tier** from `.sav` parsing (offline players, deep Pal
  data, full guild rosters). **[decided]**
- **Backup & restore**: automatic pre-commit backups, scheduled backups, a
  browsable backup list, and an orchestrated live restore workflow (see
  §6.8). **[decided]**
- Crash-restart and memory-threshold-restart supervision (leak mitigation).
  **[decided]** (1.0 shipped memory-leak fixes for dedicated servers, but
  supervision remains warranted; re-validate thresholds against 1.0
  behavior. **[confirmed fixes shipped; open on new baseline.]**)

### Out of scope (v1)

- **Multiple server instances on one host.** Explicitly deferred. Feasible
  **[confirmed]** but pushes toward a container-orchestration design that
  changes the whole backend; not needed now. **[decided]**
- **Windows hosts.** Linux only. **[decided]**
- **Docker-based servers.** The tool detects containerized servers and
  declines to adopt them (see §7.4). A Docker control path, if ever built,
  would be a parallel design (talk to the Docker API, edit compose env vars),
  not an extension of the systemd path. **[decided]**
- **Editing `WorldOption.sav`.** The tool links out to an external file
  builder for this, as PST does. See §6.2 and the **[open]** item in §11.
  **[decided]**
- **RBAC / multi-user administration.** Single admin credential for v1;
  architected so it can be added later without a rewrite (see §6.6).
  **[decided]**

---

## 4. Licensing & prior art

- **PST (palworld-server-tool):** Apache 2.0. **[confirmed]** Reusable —
  its `.sav` parsing (the hardest component) and map assets can be linked or
  its `sav_cli` helper shelled out to, with attribution. This is the basis
  for the visibility layer.
- **palworld-admin:** "Expressed Permission Only" / proprietary, despite
  public source on GitHub. **[confirmed]** Not open source. Learn from its
  behavior; **do not copy or fork its code**. Its server-control features are
  reimplemented from scratch against the public REST/RCON APIs (which belong
  to Pocketpair, not to palworld-admin), which is clean.
- **Paladin's own license: RESOLVED — Apache 2.0** (committed to the repo,
  2026-07-27). Rationale as originally leaned: permissive (fork-and-carry-
  forward friendly, matching the no-perpetual-maintenance intent), matches
  the primary dependency (PST's parser is Apache 2.0) so reuse is
  frictionless, and carries an explicit patent grant that MIT lacks — which
  matters for a tool others deploy. **[decided]** Standing caveat: map
  tiles and all game imagery are © Pocketpair and are NOT covered by the
  project license; the repo must say so wherever assets appear.

> Note: license summaries above are read from project metadata, not legal
> advice. If usage ever extends beyond personal/self-hosted use, the
> palworld-admin terms in particular warrant a direct read.

---

## 4a. Landscape & differentiation

The "Palworld server manager" space is crowded — many similarly-named
open-source projects exist, and some overlap Paladin's design substantially.
**[confirmed via search.]** Notable neighbors:

- **amantu-qbit/palworld-server-manager** — open-source, REST-API-based,
  real-time dashboard, live map, player management, admin console, LAN-use by
  design (binds 127.0.0.1, credentials only sent to the configured server).
  Large overlap with Paladin's visibility + player-admin + security posture.
- **PrakashMandal-IV/palworld-server-manager** — Windows + Linux, adopt-existing
  server, SteamCMD provision, crash guardian, auto-update with in-game warning.
  Overlaps the adopt flow, supervision, and broadcast-before-restart.
- **james-haddock/palworld-server-manager** — web UI, setup wizard, kick/ban,
  settings-via-reboot, backups.

**Implication — [inference]:** "dashboard + player admin + adopt-existing"
is not itself a differentiator; those exist. Paladin's sharper, less-common
angles are:
1. **Maintainable-by-data settings layer** with a complete, current v1.0 key
   list (the gap palworld-admin has and others don't fully close).
2. **Honest gotcha-surfacing** (e.g. base-cap level-gating) rather than
   silent "applied."
3. **Transactional commit-and-restart with post-restart verification** —
   announce → save → stop → backup → apply → start → verify as one atomic,
   verified unit.

Before build, worth reviewing amantu-qbit's project specifically — not to
copy, but to see where an existing open-source tool stops, so Paladin's
scope pushes past it rather than duplicating it. **[inference]**

Name availability: no existing Palworld tool named "Paladin" / "palworld-paladin"
surfaced in search **[confirmed no Palworld collision]**. However, the bare
name "paladin" is heavily used by unrelated GitHub projects (a Linux
Foundation blockchain platform, a bioinformatics tool, a Haiku IDE, security
vendors) **[confirmed]**, so the bare module path `paladin` sits in a noisy
namespace. **Resolution: display name "Paladin"; repo/module
`palworld-paladin`** (leads with the game name for discoverability and dodges
the crowded bare name). `pal-adin` is an acceptable alternative if the pun in
the repo name is preferred. **Repo home settled: the project lives under the
operator's GitHub account as `github.com/juicetheforce/palworld-paladin`.**
Verify the exact path is free at creation (near-certain — repo names only
need uniqueness within an account). **[decided]**

### Prior art to reuse vs. reference (licensing-critical)

- **PST — Apache 2.0 — REUSABLE code.** Borrow save parsing / `sav_cli`
  freely regardless of Paladin's own license. **[confirmed license.]**
- **uitok/PalPanel — GPL-3.0 — REFERENCE ONLY, do not copy code.** GPL is
  copyleft: copying its code forces Paladin to be GPL-3.0, which collides
  with the permissive/Apache direction. **[confirmed license.]** Its real
  value is architectural — it independently arrived at nearly Paladin's exact
  design (Go backend + `sav-cli` sidecar + SQLite + systemd, `/opt` +
  `/etc` + `/var/lib` layout, one-line curl installer, LAN-bind default, ini
  editing + lifecycle + visibility). **Read it to learn how those pieces wire
  together; reimplement in Paladin's own code.** That keeps licensing
  flexible while still shortening the cycle — the design lesson is the value,
  not the lines. **[decided]**
- **RNZ01/palworld-server-dashboard — MIT — reusable but limited scope.**
  Monitoring + player-admin only (no ini editing, no lifecycle); Next.js/Node,
  not a single binary. Useful as a reference for the visibility/player-admin
  UX and a clean player-moderation flow. **[confirmed.]**
- **amantu-qbit/palworld-server-manager — MIT — UI-design reference (and
  code-reference-safe).** Ruled out as a *tool* (Tauri desktop shell → Windows;
  not Linux-web). But its **React frontend is the UI direction Paladin
  wants**: simple, single-screen, everything accessible at a glance. MIT means
  its application code can be referenced/adapted, not just its look — subject
  to respecting that its bundled **game assets/map artwork are © Pocketpair**
  (not relicensable) and its map/coordinate work is ported from
  `palworld-save-pal`. **Take the layout/UX as Paladin's design target.**
  **[confirmed license and stack.]**

**Save-format lineage note — [confirmed]:** the community source-of-truth for
Palworld save parsing and world→map coordinate conversion is a small shared
set — **PST, `palworld-save-tools`, `palworld-save-pal`**. amantu-qbit's map
coordinate math is ported from `palworld-save-pal`. Reference these for both
the parser and the live-map coordinate conversion rather than reinventing.

**Key finding from the landscape scan:** no English-language, Linux-native
tool covers Paladin's full scope (ini editing + lifecycle + visibility).
uitok covers it but is Chinese-only (ruled out for this operator); the
English options are either monitoring-only (RNZ01) or Windows-desktop
(Prakash, amantu-qbit, Nothinx, TRRabbit). **The gap Paladin fills is real
and still open.** **[confirmed via landscape scan.]**

### Reference map (what Paladin takes from where)

| Layer | Source | License | How used |
|-------|--------|---------|----------|
| UI design / layout | amantu-qbit | MIT | Design target — simple, single-screen React UI; code-reference-safe |
| Frontend framework | (React) | — | React + TypeScript, served by the Go backend |
| Save parser | PST | Apache 2.0 | **Reuse the parser code** (parser only, not PST's UI) |
| Architecture (sidecar, systemd, installer, layout) | uitok/PalPanel | GPL-3.0 | **Reference only — do NOT copy code** (would force GPL) |
| Player-admin / moderation UX | RNZ01 | MIT | Reference for the moderation flow |
| Coordinate math (live map) | palworld-save-pal / palworld-save-tools | (verify) | Reference for world→map conversion |

The only copyleft trap is uitok (GPL): learn its architecture, write Paladin's
own code. Everything else is permissive or reference-safe. **[decided]**

---

## 5. Architecture overview

### 5.1 Tech stack

- **Backend: Go, single static binary.** **[decided]** Rationale: the
  hardest component (binary save parsing) has a working Apache-2.0
  implementation in Go (PST); reuse minimizes both initial build and ongoing
  parser maintenance (pull PST's fixes rather than reverse-engineer format
  changes). Go's single-binary deployment also minimizes install and
  troubleshooting surface on target hosts — no runtime, no dependency
  resolution. **[inference on the deployment/maintenance advantages;
  confirmed on PST being Go/Apache-2.0.]**
- **Save parsing: reuse PST's parser (Apache 2.0) — the PARSER ONLY, not
  PST's UI or backend structure.** The `.sav` binary format is the hardest,
  most patch-fragile component; PST has a battle-tested, license-safe
  implementation. Structure it as a **separate `sav-cli`-style sidecar
  process** that returns JSON (the pattern uitok uses, to keep the native
  Oodle parser decoupled from the main process). So: PST's parser code +
  uitok's sidecar architecture. Exact linking-vs-shelling detail **[open]**,
  but sidecar is the decided shape. **[decided]** Note: the broader
  save-format lineage in this ecosystem (PST, `palworld-save-tools`,
  `palworld-save-pal`) is the community source of truth for parsing and
  world->map coordinate math — reference these for the map layer too.
  **[confirmed the lineage.]**
- **Server APIs:** **REST API primary, RCON fallback.** Pocketpair has
  deprecated RCON in favor of REST. **[confirmed]** The complete official
  v1.0 REST surface is 13 endpoints: `info`, `players`, `settings`,
  `announce`, `kick`, `ban`, `unban`, `save`, `shutdown`, `stop`,
  `game-data`, `metrics`, plus the API introduction. **[confirmed against
  official 1.0 docs, 2026-07-27.]** REST is used for broadcast, player
  list, metrics, save, effective-settings readback (`GET /v1/api/settings`
  is read-only readback — exactly what the commit VERIFY step needs
  **[confirmed]**), player moderation, and the live world snapshot;
  RCON is a compatibility fallback only.
- **`/v1/api/game-data` (new in 1.0) — architecturally significant.**
  Returns a world actor snapshot: server FPS / average FPS plus an array of
  actors — Characters (`Player`, `OtomoPal`, `BaseCampPal`, `WildPal`,
  `NPC`) and `PalBox` actors — each with nickname, owner/trainer info,
  player `userid` and IP, level, HP/MaxHP, guild ID/name, class, current
  action, and X/Y/Z location + rotation. **[confirmed against official
  docs — BUT verified ABSENT from the live build: authenticated requests
  return 404 on v1.0.1.100619 (tested 2026-07-27, all plausible path
  variants).]** Docs are ahead of the shipped binary. Until the endpoint
  actually ships, live map / actor data comes from the historical tier
  (sav parsing) and `palapi` re-probes `/game-data` after every game
  update; when it answers, the live tier reclaims the map with a source
  swap behind the `vis` interface — no redesign. **[decided]**
- **Frontend: React** (with TypeScript), served as a built bundle by the Go
  backend. **[decided]** React does not conflict with the Go backend — it is
  the common stack across the reference projects (uitok, RNZ01, and
  amantu-qbit are all Go/React or React). **Dark mode is a first-class
  requirement and the default theme** (operator preference; light-only is a
  non-starter). A theme system is reasonable (RNZ01 ships several), but
  dark-by-default is the baseline. **[decided]**
- **Process management:** systemd units, one for the tool and one for the
  game server, both authored/owned by the tool. **[decided]**

### 5.2 Identity & permission model (Model A)

- **One dedicated service account** runs both the tool and the game server.
  **[decided]** This gives the tool native read access to all save/config
  files (the most frequent, most failure-prone operations), eliminating the
  permission friction that a split-user model creates.
- **Installer runs with sudo.** It creates the service account (a system
  account with no interactive human password), writes both systemd units,
  and installs a narrowly-scoped privilege grant. **[decided]**
- **Scoped service control.** The service account is granted permission —
  via a tightly-scoped sudoers rule or polkit policy — to run **only**
  `systemctl start/stop/restart/status` against **only** the server's own
  unit. Not blanket sudo. This is the one privilege exception in an
  otherwise unprivileged identity. **[decided; exact mechanism
  (sudoers vs polkit) open.]**
- **Three distinct identities — never conflate:**
  1. **Service account** (e.g. `palworld`) — OS runtime identity for tool +
     server. No human password. Created on new-install; *detected* on adopt.
  2. **Web login** — application-level credential the operator sets in the
     wizard. Stored (hashed) in the tool's own database, fully decoupled
     from any OS user. A forgotten web password is a tool-level reset, never
     an OS operation.
  3. **Invoking human** (sudo operator) — relevant only during install.
  **[decided]**

### 5.3 Runtime model

- SteamCMD, the game server, and the tool all run under the single
  unprivileged service account. SteamCMD installs into that account's home;
  the game runs as that account; the tool runs as that account and controls
  the game's unit through the scoped grant. One unprivileged identity, one
  small scoped exception. **[decided; inference that SteamCMD runs fine
  unprivileged — standard but worth confirming on first build.]**

### 5.4 Repository structure

> Added at revision 6. Designed to preserve future flexibility: all
> application code lives under Go's `internal/` (no public API = no
> structural promises), and every still-[open] question that touches
> structure is contained behind an interface or a template pair rather
> than a directory commitment. **[decided]**

```
palworld-paladin/
├── cmd/paladin/main.go        # entrypoint; wiring only
├── internal/
│   ├── palapi/                # Palworld REST client (13 endpoints) + RCON fallback
│   ├── supervise/             # unit control, readiness, crash/RAM watch
│   ├── maintain/              # shared state machine (§6.9), journal, lock
│   ├── settings/              # ini parse/serialize, key-list, validation,
│   │                          # WorldOption.sav detection
│   ├── backup/                # §6.8: create/browse/retention/integrity
│   ├── players/               # §6.7: roster, banlist.txt, kick/ban/unban
│   ├── vis/                   # live tier (game-data poller) + historical tier
│   │   └── savparse/          # sav access behind ONE interface (link-PST vs
│   │                          # sidecar both fit; §11 item stays open cheaply)
│   ├── store/                 # SQLite: users, sessions, metrics, maint. log
│   ├── webserv/               # Paladin's own HTTP API + auth + static serving
│   └── onboard/               # §7: detection, adopt, new-install
├── data/palworld-settings.json  # step-0 artifact, embedded via go:embed
├── web/                       # React + TS frontend; built bundle embedded
├── deploy/
│   ├── install.sh             # §7.1 sudo installer
│   ├── systemd/               # paladin.service, palworld.service templates
│   └── grants/                # sudoers AND polkit templates (ship the winner)
├── docs/                      # this scope doc lives here as DESIGN.md
├── .github/workflows/         # CI: build, test, lint, release
├── go.mod / LICENSE / README.md
```

Naming note: `palapi` (the *game's* API client) vs `webserv` (Paladin's
*own* web API) — deliberately distinct names for two different APIs.
`go:embed` keeps the single-binary deployment while the settings key list
stays an editable, community-PR-able data file. **[decided]**

---

## 6. Core modules

### 6.1 Process supervision (foundation)

The most important backend component — the commit workflow's safety depends
entirely on reliable stop/start. **[inference]**

- Owns a systemd unit it authored for the server.
- Start / stop / restart / status via the scoped grant.
- **Restart-on-crash** and **restart-on-memory-threshold** baked into the
  unit / supervision loop (Palworld's known memory-leak behavior makes the
  RAM-threshold restart a real feature, not a nicety). **[decided;
  confirmed that memory leaks are a real Palworld issue.]**
- Must reliably detect actual server readiness (REST responding), not just
  "process exists," so the commit workflow can verify a clean restart.
  **[inference]**

### 6.2 Settings module

- Parse and edit the `OptionSettings=(...)` line of `PalWorldSettings.ini`.
  It is a single line of comma-separated `key=value` pairs — straightforward
  string handling, no binary format work on the critical path. **[confirmed
  that the base-cap and the missing keys are ini keys, not save-only.]**
- **Complete, current v1.0 key list** — the specific gap palworld-admin has
  (its hardcoded list predates v1.0 keys like `BaseCampMaxNumInGuild`, so it
  can't render a field for them). **[confirmed as the cause of the missing
  setting.]**
- **Data-file-driven.** A single structured file is the source of truth for
  both the form and its documentation. Per-key fields:
  - `key`, `type`, `default`, `min`/`max` (validation)
  - `tooltip` — short "what does this do" text, **owned and shipped with the
    tool** (cheap: the key-list must be maintained anyway).
  - `kb_link` — optional deep-explanation link, **out to a stable community
    source** (so rot is a one-line data fix, and community PRs can maintain
    links). Specific link target(s) **[open]** — depends on which community
    wiki is most complete/maintained for v1.0+; verify before building.
  - `gotcha` — optional flag/text for known traps (e.g. base-cap is
    level-gated), surfaced in-UI. **[decided]**
- **`WorldOption.sav`:** editing is out of scope; link to an external file
  builder (PST embeds Pal-Conf for exactly this). **[confirmed PST ships
  Pal-Conf.]** Whether the save actually overrides the ini for any live-world
  keys is **[open]** — see §11.

### 6.3 Commit-and-restart orchestrator (headline feature)

Staged edits accumulate as a pending diff; an explicit **Apply/Commit**
runs one atomic maintenance cycle. This is the tool's signature workflow and
turns "settings only apply on restart" from a limitation into a clean
transaction. **[decided; each underlying step confirmed this session.]**

> The same state machine also powers the live-restore workflow (§6.8) —
> restore is a commit cycle whose APPLY step swaps the world folder instead
> of writing the ini. Design the orchestrator once, parameterized by the
> "apply" payload. **[decided]**

Sequence (first-pass state machine — to be refined before build):

1. **PRE-CHECK** — validate the staged diff (types, ranges); confirm server
   is currently healthy (REST responding). Abort cleanly if not.
2. **ANNOUNCE** — broadcast maintenance warning(s) with a countdown
   (e.g. 5/2/1 min) via REST. **[confirmed broadcast available via REST.]**
3. **SAVE** — force a world save via REST `/v1/api/save`; wait for
   confirmation. **[confirmed]**
4. **STOP** — graceful stop via the owned unit; confirm the process is gone.
5. **BACKUP** — copy the active world folder while stopped (the tool's own
   backup, part of the atomic operation — not reliant on any external
   backup). **[decided]**
6. **APPLY** — write the ini changes. Verify file structure integrity after
   write (the `OptionSettings` line must remain well-formed).
7. **START** — start via the owned unit; wait for genuine readiness (REST up).
8. **VERIFY** — read back effective settings via REST and confirm the changed
   values took. Report honestly, including gotcha context where the effective
   value won't visibly change in-game (e.g. level-gated base cap).
   **Readback rules (verified 2026-07-27):** key matching against the
   readback is **case-insensitive** — the API returns at least one key with
   different casing than the ini (`AutoSaveSpan` → `autoSaveSpan`), and an
   exact-match comparator would report a false "didn't apply" on every
   commit. Keys marked `rest_readback: false` in the key list
   (`AdminPassword`, `ServerPassword` — never echoed by the API) are
   reported as "applied — not verifiable via readback; file is
   authoritative," never as failures.

**Failure handling / rollback** is fully specified in §6.9 (shared state
machine, invariants, and rollback matrix), which governs this workflow and
the restore workflow (§6.8) identically. **[decided at revision 3]**

### 6.4 Server control

- Start / stop / restart (owned unit). **[decided]**
- Broadcast (server-wide) — REST primary, RCON fallback. **[confirmed
  available.]**
- **Update** — stop -> SteamCMD update (app ID **2394010** **[confirmed]**) ->
  restart. **[inference on exact sequencing; standard.]**

(Player-targeted actions — kick/ban/unban/message — are their own module; see
§6.7.)

### 6.5 Visibility — two tiers (REST-live + sav-historical)

Revised at revision 2: the 1.0 `/game-data` endpoint (§5.1) splits
visibility into two tiers with very different costs and freshness.
**[decided]**

**Live tier — REST `/players` + `/metrics` (+ `/game-data` when it
ships — see §5.1: documented but absent on v1.0.1). Poll, don't persist
(with narrow exceptions).** Until `/game-data` exists, actor/map data is
served by the historical tier and the sav sidecar is load-bearing for the
map again (the pre-revision-2 posture); the tier split and interface stay
as designed so the swap-back is mechanical.
- Live map (player/Pal/base positions), live roster, live HP, guild
  affiliation, server FPS — all from officially documented, versioned REST.
  No binary parsing on this path. **[confirmed the endpoint provides this.]**
- World→map coordinate conversion is still needed (`/game-data` returns raw
  world coordinates); reference the palworld-save-pal /
  palworld-save-tools lineage for the math. **[confirmed lineage.]**
- **Polling and persistence policy [decided in shape]:** the live view is
  served straight from polls and is *not* written to disk wholesale. A raw
  snapshot of a busy world (wild Pals included) can plausibly run to
  megabytes of JSON; persisting every poll would grow gigabytes per day.
  What *is* persisted (to SQLite) is a narrow derived slice: player session
  events (join/leave, identifiers), periodic player positions (optional
  trails), base locations, and downsampled metrics — kilobytes per sample,
  tens of MB per month, with a retention/downsampling policy. **[inference
  on sizes — measure on the test box; policy defaults open.]**
- **Server-side cost of `/game-data` on a large 1.0 world is unmeasured**
  (the endpoint serializes every actor per call). Default to a modest poll
  cadence (e.g. 5–15 s for the map when the map page is open, slower when
  idle) and measure before tightening. **[open — measure on test box.]**

**Historical tier — `.sav` parsing (reuse PST's parser, sidecar shape per
§5.1). Scheduled/on-demand, not continuous.**
- Offline players, deep Pal data (stats, skills), full guild rosters, base
  details — data that only exists in the save. **[confirmed PST's
  approach.]**
- **Parsing is tool-side heavy:** parsing `Level.sav` uses roughly 1–3 GB
  of RAM for the duration of the parse (per PST's own docs), and the tool
  runs on the same host as the game server. Parse on a schedule (e.g.
  every 5–10 minutes) or on demand — never in a tight loop — and treat the
  parse as a resource event the supervisor is aware of. **[confirmed the
  RAM figure from PST docs; cadence default open.]**
- The sidecar is now *enrichment*, not load-bearing for the map — a
  deliberate reduction of the patch-fragility surface. **[decided]**

**Shared:**
- Map tiles/assets — reuse PST's (Apache 2.0). **[confirmed license.]**
- **Host metrics (incl. temperatures) — RESOLVED: host hardware temps**
  (operator clarified, 2026-07-27), i.e. CPU/system temps, not in-game
  data. Folded into a single small host-metrics component alongside reads
  Paladin already needs: host RAM (RAM-threshold restart), disk free
  (backup pre-flight), CPU load. Source: `/sys` directly (e.g.
  `/sys/class/hwmon`), no lm-sensors package dependency. **Must degrade
  gracefully:** VMs typically expose no thermal sensors — "no sensors
  detected" hides the panel and is correct behavior, not an error; the
  Proxmox test box natively exercises this path, bare-metal deployments
  get the full readout. **[decided]**

**Host metrics page (spec expanded at revision 10).** One triage view:
"is the problem the game or the host?" Every metric earns its place by
answering an operator question, annotated with Paladin's own thresholds
wherever one exists. **[decided]**
- **Memory (headline):** host total/available; **game-server process RSS**
  as its own series — this is what the RAM-threshold restart reads, so its
  chart carries a horizontal line at the configured threshold (the next
  auto-restart is visible before it happens); **swap usage as a red flag**
  (a game server touching swap stutters before anything crashes).
- **CPU:** overall load AND **hottest single core** prominently — Palworld
  is single-thread bound, so one pinned core matters more than a calm
  average; plus **steal time** (`%st` from `/proc/stat`), which on a VM is
  the only in-guest signal distinguishing "game is heavy" from
  "hypervisor is busy."
- **Disk:** free space on the install/backup volumes, gauge annotated at
  the backup pre-flight requirement (~2× world size); usage breakdown by
  owner: game install / world / Paladin backups / the game's own rolling
  backups (`bIsUseBackupSaveData`) — two backup systems, two piles, shown
  separately.
- **Temps:** as above (hwmon; hidden gracefully when absent).
- **Game-side metrics on the same page:** FPS, frame time, player count,
  in-game days, process uptime — from REST `/metrics` (confirmed working).
- **History:** charts fed by the §6.5 downsampled-metrics slice; the
  memory-growth curve with restart events annotated makes the leak (and
  the supervisor's response) self-explanatory.
- **Network — observational only (added at revision 11):** RX/TX
  throughput on the primary interface (`/sys/class/net/*/statistics`),
  shown alongside player count so traffic-vs-players correlation is
  visible at a glance. **Deliberately makes no saturation verdicts:**
  per-player Palworld bandwidth is modest (32 players is single-digit
  percent of gigabit), and the real-world bottleneck for home hosts —
  residential upload — sits upstream of the WAN link and is invisible to
  host counters, so any "% of capacity" gauge would be confidently wrong.
  Instead, a one-line tooltip teaches the failure mode: "Host counters
  can't see upstream (ISP) limits; if players report lag while this
  number is high, your upload is the suspect." **[decided]**
- **Deliberately excluded:** per-process listings, inode counts —
  questions nobody operating one game server asks.

### 6.6 Auth (single-admin, RBAC-ready shape)

- One admin credential set in the setup wizard; no user administration in v1.
  Matches the category (PST, palworld-admin both do single-admin).
  **[decided; confirmed peers do single-admin.]**
- **Store as a users table with one row**, not a single `admin_password`
  field — same effort now, but "add RBAC later" becomes "add rows + a roles
  column" instead of ripping out the auth layer. **[decided]**
- **Bind to LAN / localhost by default**, not `0.0.0.0`, so LAN-only is
  enforced rather than aspirational; external exposure is a deliberate act.
  **[decided]**

### 6.7 Live player administration

A **first-class player panel**, not a scattered set of action buttons. Live
player management is a core operator surface. **[decided]**

**Data sources — two paths, by design:**
- **Live roster** — from REST (currently-connected players, their session
  identifiers). **[confirmed live player list available via REST.]**
- **Offline / full player records** — from `.sav` parsing (offline players
  are not in the live roster). This is what enables acting on a player who
  isn't currently connected, and viewing historical/offline player info.
  **[inference — same parsing source as the visibility layer.]**

**Panel capabilities:**
- **Roster view** — live players with their identifiers and session info.
- **Offline player info** — browse known players from save data.
- **Kick** — disconnect a connected player (rejoinable). **[confirmed
  capability.]**
- **Ban** — persistent removal. **Mechanism resolved [confirmed, official
  1.0 docs]:** `POST /v1/api/ban` with a required `userid` body field and
  an optional `message` shown to the banned player; `POST /v1/api/unban`
  takes the same `userid`. The identifier is the Steam ID in
  `steam_7656...` format (as returned by `/v1/api/players`). Bans persist
  to `Pal/Saved/SaveGames/banlist.txt` on the server. **[confirmed the
  file path via multiple current admin references; verify format on the
  test box.]** Design consequences:
  - Online ban: roster row → `POST /ban` with the roster's `userid`.
  - Offline ban: `POST /ban` with the Steam ID recovered from save data
    (historical tier). **[CONFIRMED on the test box, 2026-07-27: the API
    accepts a never-connected userid with HTTP 200 and persists it.]**
    Captured mechanics: `banlist.txt` is created on first ban at
    `Pal/Saved/SaveGames/banlist.txt`; line format is
    `steam_<id>,<32-hex-second-field>` (second field is opaque — parse it,
    never interpret it); `/unban` blanks the entry (empty file = empty
    list, not an error). **[confirmed]**
  - Ban-list UI: read `banlist.txt` directly to render the visible,
    reversible ban list; issue unban via REST. **[decided]**
- **Unban / ban-list management** — a visible, reversible ban list. A ban
  feature without a visible unban path is half a feature. **[decided]**
- **Direct message / whisper — RESOLVED: CUT from v1.** The complete
  official 1.0 REST surface (§5.1) contains no per-player message endpoint;
  `announce` is server-wide only. **[confirmed.]** Rather than ship the
  cosmetic fallback (a broadcast addressed to a player by name), whisper is
  cut. Revisit only if Pocketpair adds an endpoint. **[decided]**

### 6.8 Backup & restore (explicit first-class module)

> Added at revision 2. Backups previously appeared only as a *step* inside
> the commit workflow (§6.3 step 5); restore did not appear at all. Both
> are now explicit features — a backup system without a restore path is
> half a feature, exactly like ban without unban.

**Backup side:**
- **Pre-commit backups** — every commit-and-restart cycle produces one
  (already in §6.3; unchanged). **[decided]**
- **Pre-restore safety backups** — every restore backs up the current
  world first, so a restore is itself reversible (see workflow below).
  **[decided]**
- **Scheduled backups** — periodic world-folder backups while running.
  Whether a consistent hot copy requires a REST `save` + copy, or a brief
  stop, is **[open — verify save-file consistency semantics on test box]**;
  the safe default is `save` via REST, then copy.
- **Backup browser** — a UI list of all backups with timestamp, trigger
  (pre-commit / pre-restore / scheduled / manual), world name, size, and
  retention state. **[decided]**
- **Retention policy** — configurable count/age-based pruning so backups
  don't eat the disk (a world folder is copied wholesale each time).
  Defaults **[open]**. **[decided that retention exists]**

**Restore side — the live restore orchestrator [decided]:**
Restore reuses the §6.3 state machine — it *is* a commit-and-restart cycle
where the APPLY step swaps the world folder instead of editing the ini.
One state machine, two workflows; build it once.

1. **SELECT** — operator picks a backup from the browser; UI shows what
   will be restored and how much world time will be lost.
2. **PRE-CHECK** — backup archive integrity check; server healthy.
3. **ANNOUNCE** — broadcast maintenance countdown via REST, with status
   updates surfaced live in the UI throughout the run.
4. **SAVE** — force a world save (so the safety backup is current).
5. **STOP** — graceful stop via the owned unit; confirm process gone.
6. **SAFETY-BACKUP** — copy the *current* world aside (tagged
   `pre-restore`), so even a restore to the wrong point is recoverable.
7. **RESTORE** — replace the active world folder from the selected backup;
   verify file-level integrity after copy.
8. **START** — start via the owned unit; wait for genuine readiness.
9. **VERIFY** — confirm the server is serving the restored world (world
   ID / save timestamp readback where available) and report honestly.

### 6.9 Shared maintenance state machine — invariants & rollback matrix

> Added at revision 3. This resolves the former [open] "rollback matrix"
> item and governs BOTH workflows that use the state machine: settings
> commit (§6.3) and live restore (§6.8). Backup/restore is the tool's
> trust-critical surface; the failure paths below are not edge cases — they
> are the feature. **[decided]**

**Invariants (hold for every cycle, both workflows):**

- **I1 — Single-flight.** One maintenance cycle at a time, enforced by a
  lock. A second request queues or is refused, never interleaved.
- **I2 — Supervision suspended.** Crash-restart / RAM-restart supervision
  is disabled for the duration of the cycle and re-enabled when the cycle
  closes (success *or* failure). Otherwise the supervisor "helpfully"
  relaunches the server mid-world-swap. This is a known trap; it must be
  structural, not remembered.
- **I3 — Journal.** The cycle writes its intent and per-step progress to
  disk *before* each mutating step. On tool startup, an unclosed journal
  means the tool crashed mid-cycle: enter recovery mode — report the exact
  step reached and the recovery anchors; never silently resume mutations.
- **I4 — Disk pre-flight.** Any cycle that copies the world requires free
  space ≥ ~2× world size plus headroom, checked at PRE-CHECK.
- **I5 — No unpreserved overwrite.** Nothing is destroyed before STOP
  completes, and every overwrite (ini or world folder) has a preserved
  prior copy before the write happens. Consequence: every failure state is
  recoverable by hand even if all automation fails.
- **I6 — Timeouts everywhere.** Every step has a timeout; a timeout is a
  failure handled per the matrix. (Exact values **[open]** — tune on the
  test box.)
- **I7 — Anchors are named.** Every failure report lists the recovery
  anchors by absolute path: the pre-write ini copy, the pre-commit backup,
  the pre-restore safety backup.
- **I8 — Live status + audit trail.** The state machine emits a progress
  event per step to the UI (countdown, "saving world," "swapping world
  folder," …) and persists the same trail as a browsable maintenance log.

**Restore APPLY mechanics (makes I5 cheap):** the active world folder is
*renamed aside* — that rename **is** the pre-restore safety backup (atomic
move, same filesystem, no copy cost) — then the selected backup is copied
into place and integrity-verified. The rename target is a dot-prefixed
scratch dir the world detector ignores; once the restored world verifies
healthy, the safety copy is RELOCATED out of the game tree into Paladin's
own root (a plain move when same-filesystem, else copy-then-delete — safe
because the restore already succeeded). This keeps the "removable guest"
principle: no Paladin artifacts are left loitering in the Steam/Palworld
save tree. **[decided at revision 13]**

**Rollback matrix:**

| Step | Failure | Automatic action | End state | Operator |
|---|---|---|---|---|
| PRE-CHECK | Invalid diff / bad backup archive / server unhealthy / disk short | Abort. Nothing announced, nothing touched. | RUNNING, unchanged | Fix cause, retry |
| ANNOUNCE | Broadcast API fails | Abort by default (players deserve warning). | RUNNING, unchanged | Retry, or explicit "proceed unannounced" override |
| SAVE | `/save` errors or times out | Abort — never STOP on an unsaved world. | RUNNING, unchanged | Retry, or explicit force-continue (warned: safety backup misses recent progress) |
| STOP | Process won't exit in grace window | Pause cycle; never auto-SIGKILL. Present the two-option escalation dialog (below). | RUNNING (stop failed); disk untouched | Choose: **Force kill** or **Cancel job** — no third path, no timeout-default |
| BACKUP / SAFETY-BACKUP | Copy/rename error, integrity check fails | Delete partial copy; restore any rename; START unchanged world. | RUNNING, unchanged | Investigate (usually disk/permissions), retry |
| APPLY (commit) | ini write error or post-write structure validation fails | Restore ini from pre-write copy; verify; START. | RUNNING, unchanged | Report shows the rejected write |
| APPLY (restore) | Copy-in fails midway or integrity check fails | Delete partial; rename original world back; START. | RUNNING, unchanged | Pick a different backup / investigate |
| START (commit) | Unit fails or REST never ready | **One** automatic rollback: restore pre-write ini, START again. Second failure → halt. | RUNNING on old settings, or DOWN + loud alert | If DOWN: anchors named; manual recovery is always possible (I5) |
| START (restore) | Unit fails or REST never ready | **One** automatic revert: safety backup back in place, START again. Second failure → halt. | RUNNING on original world, or DOWN + loud alert | Same as above |
| VERIFY (commit) | A value didn't take on readback | **No auto-rollback** — server is up and healthy. Report per-key discrepancy with gotcha context. | RUNNING, changes applied | Optional one-click "revert commit" (runs a fresh full cycle from the pre-write ini) |
| VERIFY (restore) | World identity / save timestamp readback looks wrong | **No auto-action** — server is up. Report loudly. | RUNNING, suspect world | One-click revert cycle to the safety backup |

**Double-fault rule:** any failure *during* a rollback action halts all
automation immediately — clear state report, anchors named, operator
required. The tool never chains rollbacks or retries in a loop; I5
guarantees hand-recovery is always possible. **[decided]**

**STOP escalation — user-initiated only [decided at revision 4]:**
If the server process doesn't exit within the grace window, the cycle
pauses and the UI presents exactly two options with plain-language
context, designed so the operator doesn't panic:

> *"The server didn't shut down within N seconds. It may be hung, or just
> slow. The world was already saved at the start of this job, so a force
> kill will not lose recent progress — it ends the process immediately
> instead of waiting for a graceful exit."*
>
> - **Force kill and continue** — SIGKILL the process, confirm it's gone,
>   and resume the job from the BACKUP step as normal.
> - **Cancel the job** — abort the entire cycle. Nothing on disk was
>   touched; the server is left in whatever state it's in (still running,
>   or hung), supervision is re-enabled, and the journal is closed as
>   aborted with a note that the server may need attention.

There is no auto-escalation, no configuration option to enable one, and no
timeout that picks a default — the dialog waits for the operator. Rationale:
SIGKILL is the one action in the cycle that mimics a crash, and the operator
should always be the one who decides to take it, with the reassurance (world
already saved) in front of them when they do. **[decided]**

**Remaining [open] within this section:** exact timeout values; backup
integrity-verification method (file manifest + sizes vs checksums —
checksums cost time on multi-GB worlds).

---

## 7. Onboarding flow

### 7.1 Installer (single CLI, sudo)

`sudo ./install.sh` (or curl-pipe-with-sudo). As root it:
1. Creates the dedicated service account (default name `palworld`,
   installer-overridable — see §7.3 for the adopt-time collision concern).
2. Lays down the tool binary (+ `sav_cli` if reused) and writes the tool's
   own systemd unit.
3. Installs the scoped systemctl grant.
4. Starts the tool, prints `browse to http://<host>:<port>` to continue.
**[decided; standard installer pattern — inference on details.]**

### 7.2 Web wizard — first fork

Wizard opens with the branch question: **new install** or **adopt existing**.
Each branch runs pre-flight checks before the user commits. Operator sets the
**web login** here (not any OS password). **[decided]**

### 7.3 New Install branch

- **Pre-flight:** SteamCMD present (or installable), sufficient disk, target
  ports free.
- SteamCMD deploy -> generate initial config -> create and own the server's
  systemd unit (with crash/RAM restart behavior baked in) -> launch.
- Because the tool created everything, all paths, the service account, and
  the unit name are known — no discovery needed later. **[decided]**

### 7.4 Adopt branch — "detect, confirm, adopt"

Adoption is a guided **takeover**, and is **read-only until an explicit
"take over supervision" action**, so pointing the tool at a running server
never risks it until the operator consciously hands it over. **[decided]**

**Detection phase discovers (order to be finalized):**
1. **Current supervisor / launch method** — the first and most important
   probe. Cases: a foreign systemd unit, palworld-admin as launcher, a
   screen/tmux session, a bare shell, or **Docker**. **[confirmed servers are
   launched all these ways.]**
2. **Docker -> clean decline.** If the server runs in a container, stop and
   explain: container servers are configured through their compose file
   (often ini-from-env, which would silently clobber this tool's ini edits),
   and their lifecycle is managed by the Docker daemon (which this tool's
   systemd model would fight). This tool supports systemd/bare-metal servers.
   **[decided; inference that env-driven config would clobber ini edits,
   grounded in how the common images work.]**
3. **Owning user** — read from file ownership of the `Pal/Saved` tree and/or
   the UID of the running `PalServer-Linux-Shipping` process. Present "manage
   as user X?" for confirmation. Never assume a name. **[decided; confirmed
   both are readable.]** This resolves the collision problem: there is no
   canonical Palworld service account (operators name it anything), so the
   name is read, not guessed.
4. **Install / save paths** — install dir, `Pal/Saved`, and the active world
   folder under `SaveGames/0/<hash>/`.
5. **REST / RCON config + admin password** — read from the existing ini.

**Takeover action (explicit, separate step):** stop the server via its
current method, disable/neutralize the old launcher, install and start the
tool's own unit. From this point the tool is sole supervisor. **[decided]**

> Operator note: adopted service accounts may have login credentials the
> operator doesn't remember — not this tool's concern, since the tool
> operates on the account via root-granted-at-install privilege, never by
> logging in as it.

---

## 8. Security posture

- LAN/localhost bind by default; remote access is the operator's deliberate
  choice (Tailscale, etc.). **[decided]**
- Service account is unprivileged with no usable interactive password; the
  only elevation is the scoped systemctl grant. **[decided]**
- Web credential is app-managed and hashed, decoupled from OS users.
  **[decided]**
- REST/RCON endpoints and the admin password stay on localhost — never
  exposed to the WAN. (The REST API is HTTP Basic auth over an unencrypted
  socket.) **[confirmed the auth/transport nature.]**
- The tool never grants the service account broad sudo — that would be a
  security regression and matters especially for a tool others will deploy.
  **[decided]**

---

## 9. Build order

0. **Settings key list as a data artifact** (pre-code): enumerate the full
   1.0 key set — including the 17 keys added at 1.0 — with type / default /
   range / tooltip / gotcha. Pure data work, exercises no infrastructure,
   and is the project's soul. **[decided at revision 2]**
1. **REST client (all 13 endpoints, incl. `/game-data`) + process
   supervision + start/stop/broadcast.** The foundation everything depends
   on; every later module sits on the REST client.
2. **Settings editor + commit-and-restart orchestrator (incl. the backup
   step) + restore workflow.** The headline; restore rides along nearly
   free once the state machine exists (§6.8).
3. **Visibility + live player administration** — live tier first
   (`/game-data` map, roster, kick/ban/unban — no parser needed), then the
   historical tier (`.sav` sidecar — offline players, deep data).
4. **Deploy automation + crash/RAM auto-restart parity + scheduled
   backups/retention.** The most moving parts and the most patch-fragile;
   last.
**[decided]**

MVP = items 0–2 plus the live tier and player admin from item 3. The
historical tier (sav sidecar) and new-install automation (item 4) can lag;
the adopt path exercises 1–3 against a real server first. Note the live
tier moving ahead of the sav sidecar is a revision-2 change enabled by
`/game-data` — the MVP no longer depends on binary parsing at all.

---

## 10. Where the real risk lives (be honest about this)

The conceptually simple part (writing the ini) is **not** where the risk is.
The risk lives in two places, and they deserve the most care:

1. **Process-supervision reliability.** The commit workflow is only as safe
   as the tool's ability to cleanly stop, back up, and restart. This is the
   component to make robust first. **[inference]**
2. **Cross-patch maintenance.** REST endpoints and the save format shift with
   Palworld updates — and the 1.0 release proved the point in real time:
   the 1.0 save layouts changed (HP, Talent, item-slot, guild, base-camp),
   PST's saves broke, and PST shipped an updated `sav_cli` that fixed them.
   The "pull PST's fix rather than reverse-engineer" bet paid off before
   Paladin wrote a line of code. **[confirmed via PST 1.0 release notes.]**
   Revision 2 further shrinks this surface: the live tier runs on the
   *officially documented, versioned* REST API, so binary-format fragility
   is now confined to the historical tier. **[decided]**

---

## 11. Open questions / to verify before relevant module

Resolved at revision 2 (moved to §12): ban/unban mechanism (REST +
`banlist.txt`), per-player whisper (does not exist; cut), settings readback
for VERIFY (`GET /settings`, read-only).

Still open:

- **[open — materially advanced] `WorldOption.sav` override behavior.**
  Community consensus (multiple current 1.0 sources) now firmly holds that
  once a world exists, `WorldOption.sav` in the save folder is read in
  preference to the ini for the values it contains — later ini edits
  silently do nothing for those keys on that world. This upgrades the
  planning-time assertion from "asserted too confidently" to "community
  consensus," but the exact key coverage on a 1.0 dedicated server still
  needs test-box verification. Design consequence (now firm): the settings
  module must detect `WorldOption.sav` and warn per-affected-key at commit
  time, and the VERIFY step's honest reporting must account for it.
- **[open — reframed] `/game-data` availability.** Verified ABSENT
  (authenticated 404) on live v1.0.1.100619 despite official 1.0 docs.
  `palapi` re-probes after each game update; the cost measurement from the
  original open item happens if/when the endpoint ships.
- **[open] History persistence policy defaults.** Which derived slices of
  the live tier are persisted (session events, position trails, metrics),
  at what sample rate, with what retention/downsampling. Shape decided in
  §6.5; numbers open.
- **Offline ban via REST — RESOLVED (2026-07-27, test box):** accepted
  with HTTP 200 for a never-connected ID and persisted to `banlist.txt`;
  format captured in §6.7.
- **[open — method validated, verdict pending scale] Hot-backup
  consistency.** On the fresh 0.17 MB world, `save` + live copy produced
  stable, matching checksums — but the copy finished far inside the 30 s
  autosave window, so this proves the method, not safety on a multi-GB
  world. Re-run once the world has real mass (§6.8).
- **[open] Backup retention defaults** (count/age) (§6.8).
- **[open] Memory-threshold defaults post-1.0.** 1.0 shipped leak fixes;
  re-baseline RAM growth before choosing the default restart threshold.
- **[open] Community KB link target.** Which community wiki is most complete
  and most likely to stay maintained for v1.0+, to use for `kb_link` values.
- **"Temperatures" definition — RESOLVED:** host hardware temps (CPU
  etc.), per operator. See §6.5 host-metrics component.
- **[open] Save-parser integration.** Link PST's Go package directly vs shell
  out to `sav_cli` — decide on first build of the historical tier.
- **[open] Scoped-grant mechanism.** sudoers rule vs polkit policy for the
  service-control exception.
- **Project license — RESOLVED: Apache 2.0** (§4; committed 2026-07-27).
  Frontend framework resolved: React (§5.1).
- **Rollback matrix — RESOLVED at revision 3 (§6.9).** STOP escalation
  resolved at revision 4 (user-initiated force-kill dialog, or cancel —
  never automatic). Remaining narrow opens: exact timeout values and the
  backup integrity-verification method (manifest vs checksums).
- **[open] Adopt: existing-account reuse.** Confirm the takeover reuses the
  detected account cleanly rather than colliding with it (relevant to the
  real test box, which already has a `palworld` user).

---

## 12. Reference facts (confirmed this session)

- Palworld dedicated server SteamCMD app ID: **2394010**. **[confirmed]**
- Default ports: game **8211/UDP**; RCON **25575/TCP**
  (`RCONEnabled`/`RCONPort`); REST **8212/TCP**
  (`RESTAPIEnabled`/`RESTAPIPort`). RCON deprecated in favor of REST.
  **[confirmed]**
- REST auth: HTTP Basic, user `admin`, password = the server `AdminPassword`
  from the ini. **[confirmed]**
- `PalWorldSettings.ini` structure: a header line plus one long
  `OptionSettings=(...)` line; a stray newline inside the parens silently
  reverts the file to defaults. **[confirmed]**
- Multiple instances on one host: possible with distinct ports/dirs, but the
  vanilla launch script's single-config-folder assumption pushes toward
  containerization — a key reason multi-server is deferred. **[confirmed]**

Added at revision 2 (verified 2026-07-27):

- **Palworld 1.0** released 2026-07-10 (build 1.100.427), out of Early
  Access; hotfix 1.0.1 on 2026-07-15. Free update; existing saves carry
  over. **[confirmed]**
- **1.0 added ~17 new config keys** to `PalWorldSettings.ini` — the settings
  key-list artifact must capture these. **[confirmed via server-hosting 1.0
  guides; enumerate the exact keys from `DefaultPalWorldSettings.ini` on
  the test box.]**
- **Server clustering did not ship in 1.0** — announced June 2026, absent
  from the patch notes and settings; groundwork only in the binary.
  Single-server scope remains safe. **[confirmed]**
- **1.0 dedicated-server changes:** performance/memory optimizations,
  memory-leak and crash fixes, reduced save-corruption risk, in-game voice
  chat, guild roles/permissions, EOS crossplay (console/Game Pass joins
  only via the community-server browser, never direct IP). **[confirmed]**
- **Official 1.0 REST surface (13 endpoints):** `info`, `players`,
  `settings` (read-only), `announce`, `kick`, `ban`, `unban`, `save`,
  `shutdown`, `stop`, `game-data` (world actor snapshot), `metrics`.
  No per-player message endpoint. **[confirmed, official docs.]**
- **Ban details:** `POST /v1/api/ban` body `{userid, message?}`;
  `POST /v1/api/unban` body `{userid}`; `userid` format `steam_7656...`;
  bans persist to `Pal/Saved/SaveGames/banlist.txt`; no in-game command
  lists bans — the file is the source of truth. **[confirmed endpoints
  officially; file path via current admin references.]**
- **Pocketpair explicitly warns** the REST API is not designed for direct
  internet exposure — reinforces the LAN-only bind posture. **[confirmed]**
- **Behavioral findings (test box, v1.0.1.100619, 2026-07-27):**
  offline ban accepted + persisted (format in §6.7); ini-rewrite-on-
  shutdown claim NOT reproduced (marker edit survived a graceful stop,
  hash unchanged) — APPLY-after-STOP retained defensively; `/game-data`
  absent (404 authenticated, all path variants); `/save` responds in
  ~40 ms on an idle world. **[confirmed]**
- **PST is 1.0-ready:** its current releases fix 1.0-save parsing (HP,
  Talent, item-slot, guild, base-camp layouts) with an updated open-source
  `sav_cli`; parsing `Level.sav` uses ~1–3 GB RAM per parse (PST docs).
  **[confirmed]**
