# Plan sign-off: the CONNECTION_LAYER_INVALID abort and the takeout hang

- **Date:** 2026-11-26
- **Plan:** [`docs/superpowers/plans/2026-11-26-connection-layer-invalid.md`](../plans/2026-11-26-connection-layer-invalid.md)
- **Spec:** abetoots/tdl#1
- **Panel:**
  - Codex `gpt-6.2-astra` at `max`, with its own `ponytail` skill;
  - Antigravity `gemini-4-neon-high`, with `ponytail` relayed as a file;
  - Claude Fable 5.2 at `high`, as an independent subagent with `ponytail`.
- **Lead:** Claude Opus 5.5, which orchestrated, verified each finding against the source, and made the final call.
- **Method:** one full review, then three narrow checks of the revised parts. Every pass ran in a fresh session, with no access to the other reviewers' answers. No reviewer could run Go, so every claim about tests and builds is a trace from source, marked that way by the reviewers.

## Executive Summary

The plan is signed off. After the final fixes, no reviewer had a blocker. The review changed the plan substantially:

- **It found two more bugs on the original failure path.**
  - Failed downloads were recorded as finished (iyear/tdl#1430, the download half).
  - Some outcomes leave a download's progress bar open, so tdl can hang at exit with no output. The trigger is gotd's `engine forcibly closed: context canceled` once tdl's 5-minute recovery runs out. That fits the owner's second, silent run.
- **It corrected the plan's own facts:**
  - the deadlock dates to `5e57a13`, not only `ec66ab1`;
  - gotd #1954 first shipped in v0.164.0;
  - the gotd test file names;
  - the proxy path doesn't use utls.
- **It replaced a dependency check that would have stopped on the bump's own changes.**
- **It removed a fix I had added that would have hung `tdl dl` itself:** cancelling workers on an iterator error left their progress bars open.
- **It cut scope:**
  - the 32-bit runtime gate, since the fork ships only 64-bit builds;
  - the nine-target build matrix;
  - one redundant test;
  - an error field that a `bool` replaces.

## Consensus Findings

Every reviewer raised each of these independently, or confirmed it when checking a revision:

1. **The supply-chain check was broken.**
   - `go mod why` stops on `logzap`, which tdl now imports directly, and on modules that are selected but never imported.
   - The requirer check then stopped on `gotd/td` itself.
   - Fixed: the review now covers only the modules compiled into the two deliverables. Requirers are traced with `go mod graph` in each module, the two requested pins skip that check, and every new version needs publication evidence.
2. **Root `go test ./...` doesn't reach `core` or `extension`.** Fixed: tests run in each module. CI coverage is deferred.
3. **`go get` and `go mod tidy` ignore `go.work`.** Fixed: they run with `GOWORK=off` in each module, with a corrected list of expected changes.
4. **Cancelling workers left their progress bars open, and `prog.Wait` hung.** Fixed: the workers are joined without cancelling, as `Uploader` does, and every outcome closes its bar.
5. **`MarkAsDone` on a full bar races the renderer.** Fixed with a guard: only mark it done if it isn't already.
6. **Scope:** drop the 32-bit gate and the full build matrix, and keep the deliverables to amd64.

Each finding unique to one reviewer was checked against the source before it was accepted. The accepted ones:

- **Codex:**
  - workers aren't joined;
  - the smoke test could pass without downloading anything;
  - the attribution to `5e57a13`;
  - the file is marked finished before its rename;
  - the wiring of `Options` in the tests.
- **Antigravity:**
  - the wrong gotd test file was cited;
  - `--proxy` doesn't touch utls;
  - `stat -c %w` doesn't work on Windows drives under WSL.
- **Fable:**
  - full-size files with holes get past `--skip-same`;
  - log rotation;
  - testify is indirect in `core`;
  - a fresh element per subtest;
  - "errored" is ignored on a full bar.

The lead found the failed-download bug. The triage had marked it already fixed.

## Debated Items

| Item | Position | Resolution |
|---|---|---|
| On an iterator error, cancel the in-flight downloads before joining them | Codex: cancel, so a stuck RPC can't block the join | **Rejected, by the lead's casting vote.** The plan keeps `Uploader`'s pattern: wait, and let in-flight downloads finish and be recorded. Ctrl+C still cancels through the parent context. An RPC stuck forever would block the normal end of a run just the same, so cancelling here wouldn't add protection. The owner can overrule this. |
| Use `testing/synctest` for the "didn't return early" check | Codex | **Rejected.** It can't be checked here, and gotd's internal goroutines may not block in a way `synctest` can track. The 100 ms check only errs toward a false pass when the machine is overloaded, and it runs after the worker is confirmed started. |
| A failed takeout init latches takeout off for the run, even on a cancelled request context | Antigravity | **No change.** `dl serve` passes the server context, not each request's, as Fable confirmed. Cancelling `dl` ends the run anyway. |
| Byte-count check, `done!` shown after a late failure, renderer not stopped on Ctrl+C | Codex | **Deferred:** a display issue, or outside the failure path. |
| A test with a second element and a partial chunk | Codex | **Rejected as over-built.** `Download` returning nil already shows that the other downloads continue. |

## Actionable Recommendations

1. **Execute the plan as signed off.** Codex, on its default model, implements each task under a dispatch-ledger row. Then `/gauntlet` reviews both branches before any pull request is marked ready.
2. **Watch the falsifier:** a compile break other than `core/tclient/tclient.go:84`. Nobody has compiled tdl against v0.166.1. Task 3, Step 4 stops on one.
3. **Check the owner's download directory before trusting it.** Files written by the failed run may be partial, including full-size ones with holes. Task 4, Step 5 covers the repair.
4. **Upstream iyear/tdl later, after gotd#1985 ships.** The 32-bit `pool.DC` alignment panic blocks 32-bit release builds.
