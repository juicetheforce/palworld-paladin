---
description: End-of-task handoff for Ryan's claude.ai Project
---

Finish the current task and produce a handoff Ryan can paste into his
claude.ai Project.

First:
1. Run `go vet ./...`, `go test ./...`, and `gofmt -l .`. Also run the
   frontend build if `web/` changed, and `bash -n` on any changed scripts.
   Report the actual results.
2. Update `docs/status.md`. If this task made a decision or produced a
   lesson, update `docs/decisions.md` or `docs/learnings.md` too.
3. If every check passed, commit with a clear message and push to `main`.
   If any check failed, don't commit; report it. If the task prompt said not
   to commit, skip this step.

Then output one fenced markdown block titled **Paladin handoff**, containing:

- **Task:** one line.
- **Commit:** hash and message, or why nothing was committed.
- **Changed:** files and what changed in each, briefly.
- **Why:** the reasoning behind any choice that wasn't dictated by the prompt.
- **Tests:** what was added, what ran, and the results.
- **Verified vs not verified:** be explicit. Anything that needs the VM or
  the live server is *not verified* until Ryan runs it.
- **Decisions needed:** anything you stopped on or assumed.
- **Noticed, not done:** out-of-scope issues you saw.
- **Docs updated:** which ones.

After the block, give the command block for anything Ryan needs to run on
`palworld-test` or the live box, in order and labelled by host. If nothing
needs running there, say so.
