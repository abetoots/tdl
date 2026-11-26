# Fix the CONNECTION_LAYER_INVALID abort and the takeout hang: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:**
- `tdl dl` keeps going when Telegram drops a session's connection state.
- `--takeout` falls back to a normal client, instead of hanging, when the takeout session can't start.
- A file whose download fails is reported as failed, not recorded as finished.

**Architecture:** There are two independent branches and pull requests off `master` (77cb441).
- **Phase 1** fixes two tdl bugs on the original failure path, each test-first with fakes:
  - Task 1: a lock taken twice in `core/dcpool`;
  - Task 2: in `core/downloader`, failed transfers are reported as done, and workers aren't stopped when the iterator fails.
- **Phase 2** (Task 3) bumps gotd/td from v0.140.0 to v0.166.1. That version re-sends `initConnection` after `CONNECTION_LAYER_INVALID` or `CONNECTION_NOT_INITED` (gotd #1954 and #1964). Task 3 also makes the one compile fix the bump needs.
- **Task 4** is the owner's smoke test of a combined build.

**Tech Stack:** Go 1.25.8, gotd/td, golangci-lint v2.6.0 (gofumpt, gci).

**Spec:** the root-cause analysis in https://github.com/abetoots/tdl/issues/1. This plan records what was found after filing it: the v0.166.1 target, the `Logger` break, the 32-bit limitation, and the downloader bugs.

Tracking: abetoots/tdl#1

## Global Constraints

- **Go version:** the `go 1.25.8` line stays unchanged in `go.mod`, `core/go.mod` and `extension/go.mod`.
- **gotd/td pin:** exactly `v0.166.1`, in all three `go.mod` files, in one commit.
- **gotd/contrib:** stays at v0.20.0.
- **No bulk updates:** no `go get -u`, no `go work sync`.
- **Tests run in each module.** Run `go test ./...` from `.`, from `core` and from `extension`; CI's root command doesn't reach `core` or `extension`, so per-module runs are the evidence.
- **Lint:** `golangci-lint` v2.6.0 with the repo's `.golangci.yaml`, run in `.`, `core` and `extension`.
- **Builds:** `CGO_ENABLED=0`, `-trimpath`.
- **Comments:** short, lowercase `//`.
- **Commits:**
  - Use the repo's conventional style.
  - Each commit carries `Harness: <harness>/<model> <machine>`, as the owner's global agent instructions require, plus the session's attribution lines.
  - Delegated rounds follow the dispatch-ledger rules in those instructions.
- **Where things go:** branches are pushed only to `origin` (abetoots/tdl), and pull requests target `abetoots/tdl:master`. Nothing goes to iyear/tdl.
- **Execution:**
  - Each task is delegated to Codex on its default model.
  - A `/gauntlet` review runs on both branches before any pull request is marked ready.
  - The owner merges.
- **Deliverable:** working `linux/amd64` (WSL) and `windows/amd64` binaries for the owner.
- **Known limitation: 32-bit builds.** On 32-bit (386, arm), gotd v0.154.0 and later put `pool.DC.nextConn` at offset 60. That `go.uber.org/atomic.Int64` isn't 8-byte aligned, so `pool.(*DC).acquire` panics. The fix, gotd#1985, is still open. Other `go.uber.org/atomic.Int64` fields in gotd haven't been checked. The fork ships only 64-bit builds, so this isn't tested here; it's recorded in the Task 3 pull request.

## Review Focus

1. **A transfer fails partway** (for example `CONNECTION_LAYER_INVALID` on a file connection). It must be reported as failed and must not be recorded as finished. Covered by `TestDownloadReportsFailedTransfer` in Task 2.
2. **The iterator fails while downloads are still running.** Workers must stop before `Download` returns, so the pool is never closed under them. Covered by `TestDownloadIterErrorWaitsForWorkers` in Task 2.
3. **`--takeout` with a failing init while other downloads call the pool at the same time.** Every caller must return and get a working client, and init must run once. Covered by `TestTakeoutConcurrentCallers` in Task 1.
4. **Takeout cleanup at exit.** A run whose takeout init failed must not send `account.finishTakeoutSession`; a run whose init succeeded sends it once, wrapped. Covered by `TestCloseFinishesTakeoutOnlyWhenStarted` in Task 1.
5. **A session saved by a v0.140.0 build** must still load after the bump. Covered by gotd's `TestDataOldFormat` in Task 3, Step 6, and by Task 4.

---

### Task 0: Toolchain and baseline

This machine has no Go toolchain.

- [ ] **Step 1: Install Go 1.25.8 user-locally (no sudo).**
  - Download `go1.25.8.linux-amd64.tar.gz` from https://go.dev/dl/.
  - Check its sha256 against `https://go.dev/dl/?mode=json&include=all`.
  - Extract it to `~/.local/go1.25.8` and put its `bin` on `PATH`.
  - Expected: `go version` prints `go version go1.25.8 linux/amd64`.
- [ ] **Step 2: Install golangci-lint v2.6.0.**
  - Run: `GOBIN=$HOME/.local/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.6.0`.
  - Expected: `golangci-lint version` reports 2.6.0.
- [ ] **Step 3: Record the baseline on `master`.** Record every failure in the tracking notes as pre-existing; don't fix it.
  - In each of `.`, `core` and `extension`, run `go build ./...`, `go test ./...` and `golangci-lint run`.
  - In each module, run `GOWORK=off go mod tidy -diff`. This shows any drift that exists before the bump, so it isn't mixed into the bump commit.

### Task 1: Takeout fallback without a deadlock (Phase 1)

Branch: `fix/dl-failure-path`, off `master`.

**Cause:**
- `5e57a13` (2023-05-31) made `Takeout()` hold `p.mu` and fall back to `p.Client()` when init fails.
- Back then `invoker()` took `p.mu` as well, so both paths locked twice.
- `ec66ab1` (2024-10-03) moved the lock into `Client()`. That fixed the success path; the failure path still locks twice.
- The mutex isn't reentrant, so the first failed `account.initTakeoutSession` hangs, and every later `pool.Client`, `Default` or `Takeout` call blocks behind it.

**Files:**
- Modify: `core/dcpool/dcpool.go` (the `pool` struct, `NewPool`, `Takeout`)
- Create: `core/dcpool/dcpool_test.go`

**Interfaces:**
- Produces (unexported, package `dcpool`):
  - `type apiClient interface { tg.Invoker; Config() tg.Config; Pool(max int64) (telegram.CloseInvoker, error); DC(ctx context.Context, dc int, max int64) (telegram.CloseInvoker, error) }`. This is the subset of `*telegram.Client` the pool uses, unchanged between gotd v0.140.0 and v0.166.1.
  - `func newPool(c apiClient, size int64, middlewares ...telegram.Middleware) *pool`
  - `pool.api` becomes `apiClient`.
  - New field `pool.takeoutFailed bool`: a failed init isn't retried in the same run.
- Unchanged: `NewPool(c *telegram.Client, size int64, middlewares ...telegram.Middleware) Pool` and the `Pool` interface.

- [ ] **Step 1: Add the seam, as a pure refactor.**
  - Add `apiClient` and `newPool`. `NewPool` returns `newPool(c, size, middlewares...)`.
  - Run: `go build ./... && go test ./...` in `core`.
  - Expected: both pass.
- [ ] **Step 2: Write the failing tests in `core/dcpool/dcpool_test.go`.** The test file uses `package dcpool`.
  - **Fakes:**
    - `fakeAPI` implements `apiClient`:
      - `Config()` returns `tg.Config{ThisDC: 2}`.
      - `Pool` returns its `*fakeInvoker`.
      - `DC` calls `t.Errorf`.
      - `Invoke` counts `*tg.AccountInitTakeoutSessionRequest` calls with an atomic counter. It then either returns `initErr`, or encodes `&tg.AccountTakeout{ID: 42}` into a `bin.Buffer` and calls `output.Decode`. Any other request returns an error.
    - `fakeInvoker` implements `telegram.CloseInvoker`. It records every input under a mutex and returns `errStub`. `Close` counts its calls and returns nil.
    - Helper `takeoutWithin(t, p, 2*time.Second) *tg.Client`: runs `p.Takeout(ctx, 2)` in a goroutine and calls `t.Fatal("Takeout deadlocked")` on timeout.
    - The failing init error is `tgerr.New(420, "TAKEOUT_INIT_DELAY_3600")`.
  - **`TestTakeoutInitFailureFallsBack`:**
    - With the failing init, get a client from `takeoutWithin`.
    - `_, err := c.HelpGetConfig(ctx)`; assert `errors.Is(err, errStub)`.
    - Assert the only recorded input is a `*tg.HelpGetConfigRequest`.
  - **`TestTakeoutInitFailureIsNotRetried`:** with the failing init, call `takeoutWithin` twice. Assert the init count is 1.
  - **`TestTakeoutConcurrentCallers`:** two subtests, `init fails` and `init succeeds`.
    - Each starts 8 goroutines that call `p.Takeout(ctx, 2)` and then `HelpGetConfig` on the client they got back. It waits on a `sync.WaitGroup` with a 2-second deadline; a timeout fails with `Takeout deadlocked`.
    - Both subtests assert the init count is 1 and that 8 inputs are recorded:
      - `init fails`: each is a `*tg.HelpGetConfigRequest`;
      - `init succeeds`: each is a `*tg.InvokeWithTakeoutRequest` with `TakeoutID == 42`.
  - **`TestCloseFinishesTakeoutOnlyWhenStarted`:**
    - Subtest `failed init`: call `takeoutWithin`, then `p.Close()`. Assert:
      - `Close` returned nil;
      - no `*tg.InvokeWithTakeoutRequest` was recorded;
      - `fakeInvoker.Close` was called once.
    - Subtest `started`: call `takeoutWithin` with a succeeding init, then `p.Close()`. Assert:
      - `errors.Is(closeErr, errStub)`;
      - exactly one `*tg.InvokeWithTakeoutRequest` was recorded, with `TakeoutID == 42`;
      - its `Query`, encoded into a `bin.Buffer`, gives `id, err := buf.PeekID()` with `err == nil` and `id == tg.AccountFinishTakeoutSessionRequestTypeID`;
      - `fakeInvoker.Close` was called once.
- [ ] **Step 3: Run the tests and confirm they fail for the right reason.**
  - Run: `go test -race ./dcpool/ -v` in `core`.
  - Expected to FAIL with `Takeout deadlocked`: `TestTakeoutInitFailureFallsBack`, `TestTakeoutInitFailureIsNotRetried`, `TestTakeoutConcurrentCallers/init_fails`, `TestCloseFinishesTakeoutOnlyWhenStarted/failed_init`.
  - Expected to PASS: `TestTakeoutConcurrentCallers/init_succeeds`, `TestCloseFinishesTakeoutOnlyWhenStarted/started`.
- [ ] **Step 4: Fix `(*pool).Takeout`.** Everything happens under the one `p.mu` lock.
  - If `p.takeout == 0 && !p.takeoutFailed`, call `takeout.Takeout(ctx, p.api)`.
    - On error: set `p.takeoutFailed` and keep the existing `Warn("takeout error", ...)`.
    - On success: set `p.takeout` and keep the existing `Info`.
  - Then, if `p.takeout == 0`, return `tg.NewClient(p.invoker(ctx, dc))`, with the comment `// p.mu is held: use p.invoker, not p.Client`.
  - Otherwise, wrap the client as today.
- [ ] **Step 5: Run the tests and confirm they pass.**
  - Run: `go test -race ./dcpool/ -v` in `core`.
  - Expected: all PASS.
- [ ] **Step 6: Lint and run the full tests.**
  - `golangci-lint run` in `core`: expected no issues.
  - `go test ./...` in each module: expected to match the baseline.
- [ ] **Step 7: Commit.** Message `fix(dcpool): don't deadlock when takeout init fails`. The body names 5e57a13 and ec66ab1 and refs abetoots/tdl#1.

### Task 2: Report failed downloads and stop workers on iterator error (Phase 1)

Branch: `fix/dl-failure-path`, after Task 1.

**Cause:** in `core/downloader/downloader.go`, `Download` has two bugs.
- **Failed transfers are reported as done.**
  - A worker that hits a download error other than cancellation logs it and returns nil, and `OnDone` gets that nil.
  - `app/dl/progress.go` then marks the element finished and renames the partial temp file to its final name.
  - `--continue` skips it from then on. This is the download half of iyear/tdl#1430.
- **Workers aren't stopped when the iterator fails.** `Download` returns the iterator's error without waiting for its workers. The caller's deferred `pool.Close()` then runs while workers still use the pool. This is the path of the original `CONNECTION_LAYER_INVALID` failure.

**Files:**
- Modify: `core/downloader/downloader.go` (`Download`)
- Create: `core/downloader/downloader_test.go`

**Interfaces:**
- Unchanged: `Download(ctx context.Context, limit int) error`, and the `Iter`, `Elem`, `File` and `Progress` interfaces.
- Behaviour change for callers: `Progress.OnDone` receives the transfer's real error. `app/dl/progress.go` already handles a non-nil error: it reports the failure, removes the temp file, and doesn't mark the file finished.

- [ ] **Step 1: Write the failing tests in `core/downloader/downloader_test.go`.** The test file uses `package downloader`.
  - **Fakes:**
    - `fakeIter` yields the given elements. Before reporting the end, it waits for an optional `waitBeforeEnd` channel, with a 2-second cap. `Err()` returns a configured error.
    - `fakeElem`:
      - `File()` returns DC 2, size `1 << 20`, and location `&tg.InputDocumentFileLocation{}`;
      - `To()` returns an in-memory `io.WriterAt`;
      - `AsTakeout()` returns false.
    - `fakeProgress` records every `OnDone` error under a mutex. `OnAdd` and `OnDownload` do nothing.
    - `fakePool` implements `dcpool.Pool`: `Client`, `Default` and `Takeout` return `tg.NewClient(inv)`, and `Close` returns nil.
    - Invokers:
      - `errInvoker` returns `tgerr.New(400, "CONNECTION_LAYER_INVALID")`;
      - `blockingInvoker` closes `entered` once, then blocks until `ctx.Done()` and returns `ctx.Err()`.
    - Use `Options{Threads: 1}`.
  - **`TestDownloadReportsFailedTransfer`:**
    - One element, with `errInvoker`.
    - Assert `d.Download(ctx, 1)` returns nil, so other downloads aren't stopped.
    - Assert `OnDone` was called once, with an error where `tgerr.Is(err, "CONNECTION_LAYER_INVALID")`.
  - **`TestDownloadIterErrorWaitsForWorkers`:**
    - One element, with `blockingInvoker`. The iter's `waitBeforeEnd` is `inv.entered`, and `Err()` returns `errIter`.
    - Run `Download` in a goroutine; it must return within 2 seconds, else `t.Fatal("Download hung")`.
    - Assert `errors.Is(err, errIter)`.
    - Assert that by the time `Download` returned, `OnDone` had been called once, with `errors.Is(err, context.Canceled)`.
    - Cancel the test context in `t.Cleanup`.
- [ ] **Step 2: Run the tests and confirm they fail for the right reason.**
  - Run: `go test -race ./downloader/ -v` in `core`.
  - Expected: `TestDownloadReportsFailedTransfer` FAILS, because `OnDone`'s error is nil.
  - Expected: `TestDownloadIterErrorWaitsForWorkers` FAILS, because `OnDone` was called 0 times.
- [ ] **Step 3: Fix `Download`.**
  - Derive a cancellable context for the errgroup.
  - Each worker passes its `d.download` error to `OnDone` unchanged. It still returns the error to the errgroup only when `errors.Is(err, context.Canceled)`, and only logs other errors, as today.
  - On `Iter.Err()`: cancel, `_ = wg.Wait()`, then return `errors.Wrap(err, "iter")`. Add the comment `// stop workers before the caller closes the pool`.
- [ ] **Step 4: Run the tests and confirm they pass.**
  - Run: `go test -race ./downloader/ -v` in `core`.
  - Expected: both PASS.
- [ ] **Step 5: Lint and run the full tests.**
  - `golangci-lint run` in `core`: expected no issues.
  - `go test ./...` in each module: expected to match the baseline.
- [ ] **Step 6: Commit.** Message `fix(downloader): report failed downloads and stop workers on iterator error`. The body refs iyear/tdl#1430 (the download half) and abetoots/tdl#1.

### Task 3: Bump gotd/td to v0.166.1 (Phase 2)

Branch: `deps/gotd-v0.166.1`, off `master`.

**Why v0.166.1:**
- It has #1954 (first in v0.164.0) and #1964 (first in v0.165.0).
- It fixes two regressions from v0.165.0: #1976 (in v0.165.1) and #1984 (in v0.166.1).
- It was published 2026-11-15.

**No tdl-side regression test:** the re-init is gotd's code, and gotd tests it. Step 6 runs those tests at the pinned tag.

**Files:**
- Modify: `go.mod`, `go.sum`, `core/go.mod`, `core/go.sum`, `extension/go.mod`, `extension/go.sum`, `go.work.sum`.
- Modify: `core/tclient/tclient.go:84`. `telegram.Options.Logger` became `github.com/gotd/log.Logger` in gotd v0.154.0 (#1770).

**Interfaces:** none produced. Nothing is used from Phase 1.

- [ ] **Step 1: Record the module sets before the bump**, saved to the scratchpad. From the repo root, in workspace mode:
  - **All selected modules:** `go list -m -f '{{.Path}}@{{.Version}}' all | sort > mods-before.txt`.
  - **Modules compiled into tdl:** `for d in . core extension; do (cd $d && go list -deps -f '{{with .Module}}{{.Path}}@{{.Version}}{{end}}' ./...); done | sort -u > compiled-before.txt`.
- [ ] **Step 2: Fix the `Logger` field.** In `core/tclient/tclient.go`, use `Logger: logzap.New(logctx.From(ctx).Named("td"))` and import `github.com/gotd/log/logzap`.
- [ ] **Step 3: Resolve the new versions.** These commands ignore `go.work`, so set `GOWORK=off` explicitly.
  - In `core`: `GOWORK=off go get github.com/gotd/td@v0.166.1 github.com/gotd/log/logzap@v0.1.1`.
  - Then in `extension`, and then in `.`: `GOWORK=off go get github.com/gotd/td@v0.166.1`.
  - Then `GOWORK=off go mod tidy` in all three.
  - Expected:
    - all three `go.mod` files require `github.com/gotd/td v0.166.1`;
    - `core/go.mod` requires `github.com/gotd/log v0.1.0` and `github.com/gotd/log/logzap v0.1.1` directly;
    - root and `extension` may list `github.com/gotd/log` as `// indirect`;
    - `github.com/gotd/ige` moves to v0.3.0;
    - `gotd/contrib` stays at v0.20.0;
    - any other change matches Task 0's `tidy -diff` drift.
- [ ] **Step 4: Build, vet, test and lint, in workspace mode.**
  - In each of `.`, `core` and `extension`: `go build ./...`, `go vet ./...`, `go test ./...` and `golangci-lint run`.
  - Expected: tests and lint match the baseline.
  - **If any file other than `core/tclient/tclient.go` fails to compile, stop and report the errors.** That file is outside this round's paths.
- [ ] **Step 5: Supply-chain review.** Record the after sets the same way as Step 1.
  - **The review surface** is the compiled-in diff, `compiled-before.txt` against `compiled-after.txt`.
  - For each new or moved compiled-in `path@version`:
    - **Requirer:** `go mod graph | rg " <path>@<version>$"` must name `github.com/gotd/td@v0.166.1`, `github.com/gotd/log/logzap@v0.1.1`, or a module already in the graph.
    - **Age:** take the time from `go list -m -json <path>@<version> | jq -r .Time`. That can be the commit date, so if it's within 30 days, confirm with the module's release or tag date on its host.
    - **Stop** if it's younger than 3 days. With no evidence, list it as unverified in the pull request.
  - List the all-modules diff (`mods-before.txt` against `mods-after.txt`) in the pull request as "in go.sum only, not compiled".
  - Read the `go.sum` and `go.work.sum` diffs after Step 4's builds.
- [ ] **Step 6: Run gotd's own tests at the pinned tag.** In the gotd checkout at `v0.166.1`, run `go test -count=1 ./telegram/internal/manager/ ./session/`. Re-clone it if the scratchpad copy is gone.
  - Expected: PASS. This covers:
    - `TestConnInvokeReinitAfterNotInited` and its siblings, in `conn_reinit_test.go`;
    - `TestConnInvokeUsesConfiguredLayer`, in `conn_layer_test.go`;
    - `TestDataOldFormat`, the legacy-session test.
- [ ] **Step 7: Compile the two deliverable targets.**
  - Run `CGO_ENABLED=0 GOOS=<os> GOARCH=amd64 go build -trimpath -o /dev/null .` for `windows` and `linux`.
  - Expected: both compile.
- [ ] **Step 8: Commit.** Message: `chore(deps): bump github.com/gotd/td from 0.140.0 to 0.166.1`. The body covers:
  - **Why:** abetoots/tdl#1, gotd #1954 and #1964.
  - **The `Logger` change.**
  - **The supply-chain table from Step 5.**
  - **Release order:** with `GOWORK=off`, root and `extension` use the published `core` v0.20.4 and fail to compile against v0.166.1. A `core` release must be tagged first.
  - **The 32-bit limitation** from Global Constraints.
  - **CI covers only the root module's tests.**
  - **Behaviour changes users will notice:**
    - layer 222 → 233;
    - #1962: an unbounded peers cache by default;
    - #1971, #1974, #1975: new session JSON fields, which older builds ignore;
    - #1965: no more `get_future_salts`;
    - #1966: ping every 30–45 s;
    - #1968, #1976: a server time offset now corrects a skewed clock.

### Task 4: Owner smoke test (manual)

This needs the owner's account and can't run here.

- [ ] **Step 0: Build the smoke binaries.**
  - Make a local throwaway branch, `smoke/combined`: `master`, plus `git merge --no-ff` of both phase branches. It's never pushed.
  - Build with `CGO_ENABLED=0 go build -trimpath -ldflags "-X github.com/iyear/tdl/pkg/consts.Version=smoke-$(git rev-parse --short HEAD)"`:
    - `GOOS=windows GOARCH=amd64 … -o <scratchpad>/tdl-smoke.exe .`
    - `GOOS=linux GOARCH=amd64 … -o <scratchpad>/tdl-smoke .`
  - Check: `go version -m <binary> | grep gotd/td` shows `v0.166.1` for each.
  - Hand both to the owner. Use the binary on the side that holds the session: Windows keeps it in `%USERPROFILE%\.tdl`, WSL in `~/.tdl`. On the other side, run `tdl login` first.
- [ ] **Step 1: Back up the `.tdl` directory you'll use.** The session format gains fields; the backup lets you go back to the old build.
- [ ] **Step 2: Check the version.** `tdl-smoke version` should print `smoke-<sha>`.
- [ ] **Step 3: Run with `--restart --skip-same --debug`:** `tdl-smoke dl -f <export>.json --takeout -d <dir> --skip-same --restart --debug`.
  - Files already complete are skipped, because their name and size match Telegram's. Missing or truncated files, including any left by the failed run, are downloaded again.
  - **Pass:**
    - the command exits 0, with no `CONNECTION_LAYER_INVALID` abort;
    - at least one file was downloaded in this run, and its size matches the size Telegram shows for it;
    - the logged failures are listed (a failed file now shows as failed).
  - **Record:** whether `.tdl/log/latest.log` contains `Server lost connection state, re-sending initConnection` and `Connection re-initialized`.
  - If takeout can't start, the log shows `takeout error` and the downloads go on without takeout.
- [ ] **Step 4: If it stalls with no output for more than 2 minutes,** attach an excerpt of `latest.log` to abetoots/tdl#1. Redact dialog and message IDs and usernames first. The hang then has another cause: flood wait or connection recovery, candidates 2 and 3 in the issue.

## Owner decisions (2026-11-26)

- Tracking may publish to this public fork.
- Pull requests stay on the fork; nothing goes upstream.
- The goal is a working binary, for WSL or for Windows.
- The plan gets multi-model sign-off before any work starts.
- Codex, on its default model, implements; a `/gauntlet` review runs at the end.

## Deferred (not in this plan)

- gotd/contrib v0.20.0 → v0.26.0 (needs td ≥ v0.164.0; floodwait gains concurrent dispatch): a separate pull request.
- CI's unit-test step covers only the root module's packages. `core` and `extension` tests don't run in CI.
- 32-bit: upstreaming waits for gotd#1985. Other `go.uber.org/atomic.Int64` fields in gotd are unverified.
- `recovery` retries `context canceled` for up to `--reconnect-timeout`, or forever with `0` (iyear/tdl#854, #865). Needs a redesign.
- Retest after the bump: iyear/tdl#1071, #900, #876, #385 and #1147. #1363 is fixed by 77cb441.
- Show the takeout fallback in the terminal, not only in the log.
- iyear/tdl#1430, the upload half; iyear/tdl#1112, `chat ls` panics; gotd#1980, `dialogCommunity`.
- Watch `dl serve` memory with the unbounded peers cache (#1962).
