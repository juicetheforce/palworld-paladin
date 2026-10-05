---
description: Publish a release (build, tag, GitHub release, external check)
argument-hint: vX.Y.Z
---

Publish release $ARGUMENTS from this laptop. Only run this when Ryan asks for
a release; never on your own initiative. A release is **done only when the
external check in step 7 passes**. Until then, say plainly that it isn't
published.

If any step fails, stop, report the exact output, and don't continue. Never
delete or overwrite a tag or release that already exists; ask Ryan.

1. **Version.** $ARGUMENTS must look like v0.x.y and be newer than the
   latest tag (`git tag --list 'v*' --sort=-v:refname | head -1`). It must
   not already exist locally, on origin (`git ls-remote --tags origin`), or
   as a GitHub release (`gh release view $ARGUMENTS` must fail).
2. **Tools.** `gh auth status` must show a login with `repo` scope. If it
   doesn't, stop and give Ryan the exact command (`gh auth login`); don't
   work around it.
3. **Pre-release checks.**
   - The working tree is clean, and `main` is pushed: `git fetch origin`,
     then `git rev-list origin/main..HEAD` and `HEAD..origin/main` are both
     empty.
   - `go vet ./...`, `go test ./...`, `gofmt -l .` (empty).
   - `bash -n scripts/*.sh scripts/lib/*.sh`, `scripts/test-install-archive.sh`,
     `scripts/test-installer-messages.sh`.
   - Rebuild the frontend (`cd web && npm ci && npm run build`) and confirm
     `internal/webserv/dist` has no diff. If it does, stop: `dist` is stale.
   - Diff `scripts/install.sh` against the last tag. Flag any change to the
     `sav_cli` pin or checksums, or to the sudoers grant. For a grant change,
     say how existing installs will get it, and stop for Ryan's OK.
4. **Release notes.**
   - Use the notes Ryan gave or approved. Otherwise draft them from
     `git log <last-tag>..HEAD` in Ryan's plain, slightly dry register,
     naming known gaps, and show them to Ryan before publishing.
   - End with the install/update command in a code block.
   - Write them to a file in the scratchpad.
5. **Build.** `./scripts/release.sh $ARGUMENTS`. It refuses a dirty tree.
   Then verify the artifacts locally:
   `(cd dist-release && sha256sum -c paladin_$ARGUMENTS_linux_x86_64.tar.gz.sha256)`
   and `tar -xzOf … paladin | …` (or extract to the scratchpad) so that
   `paladin version` prints exactly `paladin $ARGUMENTS`.
6. **Tag and publish.**
   - Tags are **lightweight**, matching every existing tag
     (`git cat-file -t <tag>` says `commit`): `git tag $ARGUMENTS <HEAD sha>`,
     then `git push origin $ARGUMENTS`.
   - Then:
     `gh release create $ARGUMENTS dist-release/paladin_$ARGUMENTS_linux_x86_64.tar.gz dist-release/paladin_$ARGUMENTS_linux_x86_64.tar.gz.sha256 --title "Paladin $ARGUMENTS" --notes-file <notes> --latest`
     (not `--draft`, not `--prerelease`).
7. **External check (the definition of done).** From the public API, not
   from gh's own output:
   - `curl -fsSL https://api.github.com/repos/juicetheforce/palworld-paladin/releases/latest`.
     Its `tag_name` must be $ARGUMENTS, it must be neither draft nor
     prerelease, and both assets must be listed.
   - Download both assets from their public `browser_download_url`s into the
     scratchpad and check the tarball against the downloaded `.sha256`
     (`sha256sum -c`). The extracted binary must report `paladin $ARGUMENTS`.
   - If any of this fails, the release is NOT done: report exactly what's
     wrong.
8. **Afterwards.** Update `docs/status.md` (latest release, the Unreleased
   list moved into Shipped), `CLAUDE.md`'s "Latest release" line, and
   `.project.toml` `next`. Commit and push them. Tell Ryan the only manual
   steps left: test with the public curl one-liner on a fresh VM, then
   update the live box.
