# Fix the CONNECTION_LAYER_INVALID abort and the takeout hang: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:**
- `tdl dl` keeps going when Telegram drops a session's connection state.
- `--takeout` falls back to a normal client, instead of hanging, when the takeout session can't start.

**Architecture:** There are two independent changes, each its own branch and pull request off `master` (77cb441).
- **Phase 1** fixes a lock taken twice in `core/dcpool`, test-first, using a fake API client.
- **Phase 2** bumps gotd/td from v0.140.0 to v0.166.1. That version re-sends `initConnection` after `CONNECTION_LAYER_INVALID` or `CONNECTION_NOT_INITED` (gotd #1954 and #1964). Phase 2 also includes the one compile fix the bump needs.

**Tech Stack:** Go 1.25.8, gotd/td, golangci-lint v2.6.0 (gofumpt, gci).

**Spec:** the root-cause analysis in https://github.com/abetoots/tdl/issues/1. The facts found after filing it are recorded in this plan: the v0.166.1 target, the `Logger` break, and the 32-bit atomics problem.

Tracking: abetoots/tdl#1

## Global Constraints

- **Go version:** the `go 1.25.8` line stays unchanged in `go.mod`, `core/go.mod` and `extension/go.mod`.
- **gotd/td pin:** exactly `v0.166.1`, in all three `go.mod` files, in one commit.
- **gotd/contrib:** stays at v0.20.0. Moving it is deferred.
- **No bulk updates:** no `go get -u`, no `go work sync`, no other module moved on purpose.
- **Lint:** `golangci-lint` v2.6.0 with the repo's `.golangci.yaml`, run in `.`, `core` and `extension`, the same as the CI matrix. gci section order: standard, default, `prefix(github.com/iyear/tdl)`.
- **Release builds** (`.goreleaser.yaml`): `CGO_ENABLED=0`, `-trimpath`, for linux, windows and darwin on 386, amd64, arm (v5, v6, v7), arm64, riscv64 and loong64.
- **Unit tests:** CI's command, `go test -v $(go list ./... | grep -v /test)`, run from the repo root (workspace mode, `go.work`). The e2e suite under `test/` is off in CI and out of scope.
- **Comments:** short, lowercase `//`.
- **Commits:**
  - Use the repo's conventional style (`fix(dcpool): …`, `chore(deps): …`).
  - Each commit carries `Harness: <harness>/<model> <machine>`, as the owner's global agent instructions require, plus the session's attribution lines.
  - If a round is handed to another agent, the dispatch-ledger rules in those instructions apply.
- **Owner approval:** nothing is pushed anywhere and no pull request is opened without the owner.

## Review Focus

1. **Old sessions:** a session saved by a v0.140.0 build must still load after the bump, because gotd adds per-DC keys to the session JSON. Covered by Task 3, Steps 1 to 3.
2. **32-bit builds** (386, arm) must not panic in `pool.(*DC).acquire`.
   - v0.166.1 puts `DC.nextConn`, a `go.uber.org/atomic.Int64`, at offset 60 on 32-bit, which isn't 8-byte aligned. This dates from gotd v0.154.0 (#1770), when `DC.log` became `log.Helper`.
   - The fix, gotd#1985, is still open.
   - Covered by Task 2, Step 7.
3. **Concurrent callers:** `--takeout` with a failing takeout init while other downloads are calling the pool at the same time. All callers must return, and init must run once. Covered by `TestTakeoutConcurrentCallers` in Task 1.
4. **Cleanup at exit:** a run whose takeout init failed must not send `account.finishTakeoutSession`. A run whose init succeeded must send it once, wrapped. Covered by `TestCloseFinishesTakeoutOnlyWhenStarted` in Task 1.
5. **Proxy users** (`--proxy`): they dial through gotd's changed dialer, since `mtproxy/faketls` now pulls in utls. Covered by Task 3, Step 5 if the owner uses a proxy; otherwise the PR names it as unproven.

---

### Task 0: Toolchain and baseline

This machine has no Go toolchain.

- [ ] **Step 1: Install Go 1.25.8 user-locally (no sudo).**
  - Download `go1.25.8.linux-amd64.tar.gz` from https://go.dev/dl/.
  - Check its sha256 against `https://go.dev/dl/?mode=json&include=all`.
  - Extract it to `~/.local/go1.25.8` and put `~/.local/go1.25.8/bin` on `PATH`.
  - Expected: `go version` prints `go version go1.25.8 linux/amd64`.
- [ ] **Step 2: Install golangci-lint v2.6.0.**
  - Run: `GOBIN=$HOME/.local/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.6.0`.
  - Expected: `golangci-lint version` reports 2.6.0.
- [ ] **Step 3: Record the baseline on `master`.**
  - Run `go build ./...`, the CI unit-test command, and `golangci-lint run` in `.`, `core` and `extension`.
  - Expected: the build passes. Record every other failure in the tracking notes as pre-existing; don't fix it.
- [ ] **Step 4: Check that 32-bit binaries can run here.**
  - Run: `GOARCH=386 go test ./app/dl/`.
  - Expected: PASS. If it fails with `exec format error`, record "386 runtime unproven here". Task 2, Step 7 then needs qemu-user or a CI run instead.

### Task 1: Takeout fallback without a deadlock (Phase 1)

Branch: `fix/dcpool-takeout-deadlock`, off `master`.

**Cause:**
- Commit ec66ab1 (2024-10-03) moved `p.mu` out of `invoker()` and into `Client()`.
- `Takeout()` still holds `p.mu` when its error path calls `Client()`, so the lock is taken twice.
- The mutex isn't reentrant, so the first failed `account.initTakeoutSession` hangs. Every later `pool.Client`, `Default` or `Takeout` call then blocks behind it.

**Files:**
- Modify: `core/dcpool/dcpool.go` (the `pool` struct, `NewPool`, `Takeout`)
- Create: `core/dcpool/dcpool_test.go`

**Interfaces:**
- Produces (unexported, package `dcpool`):
  - `type apiClient interface { tg.Invoker; Config() tg.Config; Pool(max int64) (telegram.CloseInvoker, error); DC(ctx context.Context, dc int, max int64) (telegram.CloseInvoker, error) }`. This is the subset of `*telegram.Client` the pool uses; all four signatures are unchanged between gotd v0.140.0 and v0.166.1.
  - `func newPool(c apiClient, size int64, middlewares ...telegram.Middleware) *pool`
  - `pool.api` becomes `apiClient`.
  - New field `pool.takeoutErr error`: the first init failure. A failed init is never retried in the same run.
- Unchanged: `NewPool(c *telegram.Client, size int64, middlewares ...telegram.Middleware) Pool` and the `Pool` interface.

- [ ] **Step 1: Add the seam, as a pure refactor.**
  - Add `apiClient` and `newPool`. `NewPool` returns `newPool(c, size, middlewares...)`.
  - Run: `go build ./... && go test ./core/...`.
  - Expected: both pass.
- [ ] **Step 2: Write the failing tests in `core/dcpool/dcpool_test.go`.** The test file uses `package dcpool`.
  - **Fakes:**
    - `fakeAPI` implements `apiClient`:
      - `Config()` returns `tg.Config{ThisDC: 2}`.
      - `Pool` returns its `*fakeInvoker`.
      - `DC` calls `t.Errorf`, since only DC 2 is used.
      - `Invoke` counts `*tg.AccountInitTakeoutSessionRequest` calls with an atomic counter. It then either returns `initErr`, or encodes `&tg.AccountTakeout{ID: 42}` into a `bin.Buffer` and calls `output.Decode`. Any other request type returns an error.
    - `fakeInvoker` implements `telegram.CloseInvoker`. It records every input under a mutex and returns `errStub`. `Close` counts its calls.
    - Helper `takeoutWithin(t, p, 2*time.Second) *tg.Client`: runs `p.Takeout(ctx, 2)` in a goroutine, and calls `t.Fatal("Takeout deadlocked")` if it doesn't return in time.
    - The failing init error is `tgerr.New(420, "TAKEOUT_INIT_DELAY_3600")`.
  - **`TestTakeoutInitFailureFallsBack`:**
    - With the failing init, get a client from `takeoutWithin`.
    - `_, err := c.HelpGetConfig(ctx)`.
    - Assert `errors.Is(err, errStub)`.
    - Assert the only recorded input is a `*tg.HelpGetConfigRequest`, not a `*tg.InvokeWithTakeoutRequest`.
  - **`TestTakeoutInitFailureIsNotRetried`:** with the failing init, call `takeoutWithin` twice. Assert the init count is 1.
  - **`TestTakeoutConcurrentCallers`:** two subtests, `init fails` and `init succeeds`.
    - Each starts 8 goroutines that call `p.Takeout(ctx, 2)` and waits on a `sync.WaitGroup` with a 2-second deadline. A timeout fails with `Takeout deadlocked`.
    - Both subtests assert the init count is 1.
    - `init succeeds` also calls `HelpGetConfig` on every returned client. Assert that 8 `*tg.InvokeWithTakeoutRequest` inputs are recorded, each with `TakeoutID == 42`.
  - **`TestTakeoutWrapsRequests`:**
    - With a succeeding init, call `HelpGetConfig` on the returned client.
    - Assert the recorded input is a `*tg.InvokeWithTakeoutRequest` with `TakeoutID == 42`.
  - **`TestCloseFinishesTakeoutOnlyWhenStarted`:**
    - Subtest `failed init`: call `takeoutWithin`, then `p.Close()`. Assert:
      - `Close` returned nil;
      - no `*tg.InvokeWithTakeoutRequest` was recorded;
      - `fakeInvoker.Close` was called once.
    - Subtest `started`: call `takeoutWithin` with a succeeding init, then `p.Close()`. Assert:
      - `errors.Is(closeErr, errStub)`;
      - exactly one `*tg.InvokeWithTakeoutRequest` was recorded, with `TakeoutID == 42`;
      - its `Query`, encoded into a `bin.Buffer`, has `PeekID() == tg.AccountFinishTakeoutSessionRequestTypeID`;
      - `fakeInvoker.Close` was called once.
- [ ] **Step 3: Run the tests and confirm they fail for the right reason.**
  - Run: `go test -race ./core/dcpool/ -v`.
  - Expected to FAIL with `Takeout deadlocked`: `TestTakeoutInitFailureFallsBack`, `TestTakeoutInitFailureIsNotRetried`, `TestTakeoutConcurrentCallers/init_fails`, `TestCloseFinishesTakeoutOnlyWhenStarted/failed_init`.
  - Expected to PASS: `TestTakeoutWrapsRequests`, `TestTakeoutConcurrentCallers/init_succeeds`, `TestCloseFinishesTakeoutOnlyWhenStarted/started`.
- [ ] **Step 4: Fix `(*pool).Takeout`.** Everything happens under the one `p.mu` lock.
  - If `p.takeout == 0 && p.takeoutErr == nil`, call `takeout.Takeout(ctx, p.api)`.
    - On error: set `p.takeoutErr` and keep the existing `Warn("takeout error", ...)`.
    - On success: set `p.takeout` and keep the existing `Info("get takeout id", ...)`.
  - Then, if `p.takeout == 0`, return `tg.NewClient(p.invoker(ctx, dc))`. Never call `p.Client` here, because it locks again. Add the comment `// p.mu is held: use p.invoker, not p.Client`.
  - Otherwise, wrap the client as today.
- [ ] **Step 5: Run the tests and confirm they pass.**
  - Run: `go test -race ./core/dcpool/ -v`. Expected: all PASS.
  - Run: `GOARCH=386 go test ./core/dcpool/`. Expected: PASS, if Task 0, Step 4 showed 386 binaries run here.
- [ ] **Step 6: Lint and run the full unit tests.**
  - `golangci-lint run` in `core`: expected no issues.
  - CI's unit-test command from the root: expected to match the baseline.
- [ ] **Step 7: Commit.** Message `fix(dcpool): don't deadlock when takeout init fails`. The body names ec66ab1 as the commit that introduced the bug, and refs abetoots/tdl#1.

### Task 2: Bump gotd/td to v0.166.1 (Phase 2)

Branch: `deps/gotd-v0.166.1`, off `master`. Task 1 isn't required.

**Why v0.166.1:**
- It has #1954 (first in v0.164.0) and #1964 (first in v0.165.0).
- It fixes two regressions from v0.165.0: #1976, an initConnection loop when the clock is ahead, and #1984, salt loss causing hourly reconnects.
- It was published 2026-11-15, which is past the 3-day age window.
- Nothing enforces that age window for Go modules; this check is manual.

**Files:**
- Modify: `go.mod`, `go.sum`, `core/go.mod`, `core/go.sum`, `extension/go.mod`, `extension/go.sum`, `go.work.sum`.
- Modify: `core/tclient/tclient.go:84`. `telegram.Options.Logger` became `github.com/gotd/log.Logger` in gotd v0.154.0 (#1770).

**Interfaces:** none produced. Nothing is used from Task 1.

**No tdl-side regression test:** the re-init behaviour is gotd's own code, and gotd tests it (`telegram/internal/manager/conn_reinit_test.go`, `TestConnInvokeUsesConfiguredLayer`). A tdl test of it would be testing the library. tdl's evidence is Steps 5–7 and the Task 3 smoke run.

- [ ] **Step 1: Record the module set before the bump.**
  - In each of `.`, `core` and `extension`, run `go list -m -f '{{.Path}}@{{.Version}}' all | sort`.
  - Save the output to the scratchpad as `mods-before-<dir>.txt`.
- [ ] **Step 2: Fix the `Logger` field.** In `core/tclient/tclient.go`, use `Logger: logzap.New(logctx.From(ctx).Named("td"))` and import `github.com/gotd/log/logzap`.
- [ ] **Step 3: Resolve the new versions.**
  - In `core`: `go get github.com/gotd/td@v0.166.1 github.com/gotd/log/logzap@v0.1.1`.
  - Then in `extension`, and then in `.`: `go get github.com/gotd/td@v0.166.1`.
  - Then run `go mod tidy` in all three.
  - Expected:
    - all three `go.mod` files require `github.com/gotd/td v0.166.1`;
    - `core/go.mod` gains `github.com/gotd/log v0.1.0` and `github.com/gotd/log/logzap v0.1.1`;
    - `github.com/gotd/ige` moves to v0.3.0;
    - `gotd/contrib` stays at v0.20.0.
- [ ] **Step 4: Supply-chain review.** The `go.sum` diff is the review surface.
  - Diff the module set after the bump against Step 1's files.
  - For each new `path@version`:
    - check it's older than 3 days: `curl -s https://proxy.golang.org/<path>/@v/<version>.info | jq -r .Time`;
    - check how it got there: `go mod why -m <path>` must show a chain through `github.com/gotd/td`.
  - Stop and report if a module is younger than 3 days, or arrives some other way.
- [ ] **Step 5: Build, vet, test and lint.**
  - `go build ./...` and `go vet ./...` in each module: expected to pass.
  - CI's unit-test command from the root: expected to match the baseline.
  - `golangci-lint run` in `.`, `core` and `extension`: expected no new issues.
- [ ] **Step 6: Compile the release targets.** This also shows the new utls dependencies are cgo-free.
  - Run `CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build -trimpath -o /dev/null .` for each of:
    - windows/amd64, windows/386;
    - linux/amd64, linux/386, linux/arm with `GOARM=7`, linux/arm64, linux/riscv64, linux/loong64;
    - darwin/arm64.
  - Expected: every target compiles.
- [ ] **Step 7: 32-bit runtime gate (Review Focus 2).**
  - In the gotd checkout at `v0.166.1`, run `GOARCH=386 go test -count=1 ./pool/`.
  - If it panics with `unaligned 64-bit atomic operation`, the bump is safe for 64-bit builds only:
    - record the panic output;
    - mark the pull request blocked until gotd releases #1985;
    - add a Deferred item dated today. 64-bit builds, including the owner's windows/amd64 build, are unaffected.
  - If it doesn't panic, record the output and carry on.
- [ ] **Step 8: Commit.** Message: `chore(deps): bump github.com/gotd/td from 0.140.0 to 0.166.1`. The body covers:
  - **Why:** abetoots/tdl#1, gotd #1954 and #1964.
  - **The `Logger` change.**
  - **The result of Step 7.**
  - **Release order:** with `GOWORK=off`, the root module uses the published `core` v0.20.4 and fails to compile against v0.166.1. A `core` release with this change must be tagged before the root module moves off `core` v0.20.4.
  - **Behaviour changes users will notice:**
    - layer 222 → 233;
    - #1962: `peers.Options.Cache` now defaults to an unbounded in-memory cache;
    - #1971, #1974, #1975: the session JSON gains fields, which older builds ignore;
    - #1965: no more `get_future_salts`;
    - #1966: ping every 30–45 s;
    - #1968, #1976: a server time offset now corrects a skewed clock.

### Task 3: Owner smoke test on Windows (manual)

This needs the owner's account and can't run here.

- [ ] **Step 0: Build the smoke binary.**
  - Make a local throwaway branch, `smoke/combined`: `master`, plus `git merge --no-ff` of both task branches. It's never pushed.
  - Build: `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-X github.com/iyear/tdl/pkg/consts.Version=smoke-$(git rev-parse --short HEAD)" -o <scratchpad>/tdl-smoke.exe .`
  - Check: `go version -m <scratchpad>/tdl-smoke.exe | grep gotd/td` shows `v0.166.1`.
  - Hand the owner the `.exe`.
- [ ] **Step 1: Back up `%USERPROFILE%\.tdl`.** The session format gains fields; the backup lets you go back to the old build.
- [ ] **Step 2: Check the version.** `tdl-smoke.exe version` should print `smoke-<sha>`.
- [ ] **Step 3: Run the original command with `--debug`.** `tdl-smoke.exe dl -f <export>.json --takeout -d <dir> --skip-same --continue --debug`.
  - **Pass:** the download completes without a `CONNECTION_LAYER_INVALID` abort.
  - If Telegram drops the session state again, `%USERPROFILE%\.tdl\log\latest.log` shows gotd's warning that the state was lost, then its re-initialized message, and the run continues.
  - If takeout can't start, the log shows `takeout error` and downloads go on without takeout.
- [ ] **Step 4: If it stalls.** If it stalls with no output for more than 2 minutes, attach `latest.log` to abetoots/tdl#1. The hang then has another cause: candidates 2 and 3 in that issue, flood wait or connection recovery.
- [ ] **Step 5: Proxy check.** If you use a proxy, repeat Step 3 with your usual `--proxy`.

## After the plan (owner decisions)

- Where Phase 1 and Phase 2 go:
  - branches pushed to `abetoots/tdl`;
  - pull requests against `iyear/tdl:master`, or only against the fork.
- If Task 2, Step 7 panics, Phase 2 stays a draft until gotd ships #1985.

## Deferred (not in this plan)

- **gotd/contrib v0.20.0 → v0.26.0** (needs td ≥ v0.164.0; floodwait gains concurrent dispatch): a separate pull request.
- **`recovery` retries `context canceled`** for up to `--reconnect-timeout`, or forever with `0`. Upstream issues iyear/tdl#854 and #865. gotd#1982 makes this more likely. Needs a redesign.
- **Retest after the bump:**
  - the connection-drop family: iyear/tdl#1071, #900, #876, #385;
  - iyear/tdl#1147, probably fixed by 1736ead;
  - iyear/tdl#1363, fixed by 77cb441.
- **Show the takeout fallback in the terminal**, not only in the log.
- **iyear/tdl#1430:** re-check failed uploads that are reported as done.
- **iyear/tdl#1112:** `chat ls` panics in `fetchTopics`.
- **gotd#1980:** `dialogCommunity` breaks `query/dialogs`.
- **Watch `dl serve` memory** with the unbounded peers cache (#1962).
