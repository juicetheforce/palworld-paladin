---
description: Prepare a release (checks and notes only; Ryan runs the train)
argument-hint: vX.Y.Z
---

Prepare release $ARGUMENTS. Do not tag, push, or build release artifacts
yourself.

1. Check that $ARGUMENTS is v0.x.y and newer than the latest tag
   (`git tag --list 'v*' --sort=-v:refname | head -1`).
2. Check that the working tree is clean, `go vet`/`go test` pass, and
   `main` is pushed (no unpushed commits).
3. Rebuild the frontend and confirm `internal/webserv/dist` has no diff. If it
   does, stop and report: `dist` is stale.
4. Diff `scripts/install.sh` against the last tag. Flag any change to the
   `sav_cli` pin or checksums, or to the sudoers grant. For grant changes,
   say how existing installs will receive them.
5. Draft honest release notes from `git log <last-tag>..HEAD`.
   - Write them in Ryan's plain, slightly dry register, and name known gaps.
   - End with the install/update command in a code block.
6. Output the release train as copy-paste blocks, labelled by host:
   - VM: `release.sh`.
   - Laptop: scp the artifacts, then create and push the tag.
   - GitHub: the release steps.
   - Live box: the update.

   Code is already pushed, so there is no laptop commit step.
7. Remind Ryan to update `docs/status.md` with the new version once the
   release is deployed.
