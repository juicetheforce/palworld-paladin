# Paladin

**A self-hosted admin panel that deploys, owns, supervises, configures, and
observes exactly one Palworld dedicated server** — from a bare Linux host to
a running, managed game server, behind a single modern web interface.

> **Status: pre-alpha.** This repository currently contains the design
> document, the settings key-list data artifact, and the project scaffold.
> No functional release exists yet.

## Why another Palworld tool?

Existing managers share two blind spots: a hardcoded settings list that goes
stale as the game updates, and no honest handling of "I changed a setting
and nothing happened." Paladin exists to close both:

- **A maintainable-by-data settings layer.** The complete v1.0 key list —
  119 keys, tooltips, validation ranges, and 49 documented gotchas — lives
  in [`data/palworld-settings.json`](data/palworld-settings.json). Keeping
  current with Palworld patches is a data edit and a community PR, not a
  code release.
- **Honest gotcha surfacing.** Level-gated caps, keys that silently don't
  work on dedicated servers, inverted semantics, deliberately-misspelled
  key names — surfaced in the UI instead of a false "applied."
- **Transactional commit-and-restart.** Settings changes stage as a diff
  and apply as one verified atomic cycle: announce → save → stop → backup →
  apply → start → verify — with a fully specified per-step rollback matrix.
- **Live restore.** Browse backups and restore from the web UI through the
  same orchestrated, reversible, loudly-honest state machine.

## Design

The full scope and design document is [`docs/DESIGN.md`](docs/DESIGN.md).
Every claim in it is confidence-tagged (`[decided]`, `[confirmed]`,
`[inference]`, `[open]`); nothing marked `[open]` may be treated as settled
during implementation.

## Stack

Go single static binary (frontend and key-list embedded via `go:embed`) ·
React + TypeScript, dark by default · systemd · SQLite · Palworld official
REST API primary, RCON fallback · save parsing via
[PST](https://github.com/zaigie/palworld-server-tool)'s Apache-2.0 parser.

## License

[Apache License 2.0](LICENSE).

**Exception — game assets:** the world map tiles and all Palworld imagery
are © Pocketpair, Inc. and are *not* covered by this project's license.
They are used here for interoperability with the game they depict.
