# Fix the CONNECTION_LAYER_INVALID abort and the takeout hang: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:**
- `tdl dl` keeps going when Telegram drops a session's connection state.
- `--takeout` falls back to a normal client, instead of hanging, when the takeout session can't start.
- A file whose download fails is reported as failed, not recorded as finished.
- `tdl dl` exits on its own: every download's progress bar closes, whatever the outcome.

**Architecture:** There are two independent branches and pull requests off `master` (77cb441).
- **Phase 1** fixes two tdl bugs on the original failure path, each test-first with fakes:
  - Task 1: a lock taken twice in `core/dcpool`;
  - Task 2: `core/downloader` reports failed transfers as done and doesn't join its workers on an iterator error. Some outcomes in `app/dl/progress.go` leave the progress bar open, so tdl can hang at exit.
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
- **Tests in `core` use only the standard `testing` package.** testify is `// indirect` in `core/go.mod`, and importing it would change that file. Root tests may use testify, as `app/dl/iter_resume_test.go` does.
- **Toolchain:** Go 1.27.4 is installed at `/usr/local/go`, which isn't on the default `PATH`. Run every `go` command with `PATH=/usr/local/go/bin:$PATH GOTOOLCHAIN=go1.25.8`. That uses the checksum-verified 1.25.8 toolchain, the version CI and the release builds read from `go.mod`.
- **Tests run in each module.**
  - Root: `go test $(go list ./... | grep -v /test)`, as CI runs it. `test/` is the end-to-end suite, which needs a live server.
  - `core` and `extension`: `go test ./...` in each.
  - Root `./...` doesn't reach `core`'s 17 packages (verified in Task 0), so per-module runs are the evidence.
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
2. **The run exits on its own, whatever happened to each file.** That includes a transfer that ends in `context.Canceled` while the run itself isn't cancelled. Every progress bar must close, or `dl.Run`'s deferred `prog.Wait` waits forever. Covered by `TestOnDoneClosesBarOnError` and `TestOnDoneSuccessClosesBar` in Task 2.
3. **`--takeout` with a failing init while other downloads call the pool at the same time.** Every caller must return and get a working client, and init must run once. Covered by `TestTakeoutConcurrentCallers` in Task 1.
4. **Takeout cleanup at exit.** A run whose takeout init failed must not send `account.finishTakeoutSession`; a run whose init succeeded sends it once, wrapped. Covered by `TestCloseFinishesTakeoutOnlyWhenStarted` in Task 1.
5. **A session saved by a v0.140.0 build** must still load after the bump. Covered by gotd's `TestDataOldFormat` in Task 3, Step 6, and by Task 4.

---

### Task 0: Toolchain and baseline

This machine has no Go toolchain.

- [x] **Step 1: Toolchain.**
  - The owner installed Go 1.27.4 at `/usr/local/go`. Its archive sha256 matches go.dev's: `7226736c…dab24840`.
  - `GOTOOLCHAIN=go1.25.8 go version` fetched and verified 1.25.8, and prints `go version go1.25.8 linux/amd64`.
- [x] **Step 2: golangci-lint v2.6.0.** Installed with `GOTOOLCHAIN=go1.25.8 GOBIN=$HOME/.local/bin go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.6.0`. It reports `version 2.6.0 built with go1.25.8`.
- [x] **Step 3: Baseline on `master` (77cb441), 2026-11-26.**
  - All three modules build.
  - Root tests pass: 9 packages pass, 18 have none. `core` (17 packages) and `extension` (1) have no tests yet.
  - `golangci-lint run`: 0 issues in each module.
  - `GOWORK=off go mod tidy -diff`:
    - Root has existing drift: `samber/lo` and `yapingcat/gomedia` move from `// indirect` to direct, and `go.sum` doesn't change.
    - `core` and `extension` are clean.

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
- [ ] **Step 2: Write the failing tests in `core/dcpool/dcpool_test.go`.** The test file uses `package dcpool` and the standard `testing` package.
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
  - The tests in each module, per Global Constraints: expected to match the baseline, plus the new tests.
- [ ] **Step 7: Commit.** Message `fix(dcpool): don't deadlock when takeout init fails`. The body names 5e57a13 and ec66ab1 and refs abetoots/tdl#1.

### Task 2: Report every download outcome and always close its progress bar (Phase 1)

Branch: `fix/dl-failure-path`, after Task 1.

**Cause:** four defects on the failure path.
1. **Failed transfers are reported as done.**
   - In `core/downloader/downloader.go`, a worker that hits a non-cancel download error logs it and returns nil, so its deferred `OnDone` gets nil.
   - `app/dl/progress.go` then records the file finished (`:78`) and renames the partial temp file to its final name (`:101`).
   - `--continue` skips it from then on. This is the download half of iyear/tdl#1430.
2. **Workers aren't joined on an iterator error.** `Download` returns without waiting, unlike `core/uploader/uploader.go:85-89`. `dl.Run`'s `prog.Wait` can then see no active bars:
   - before a worker has called `OnAdd`;
   - or after a bar fills, but before `OnDone` finishes.

   Progress is then saved without that file, and the pool is closed under it.
3. **A cancelled transfer leaves its bar open** (`app/dl/progress.go:70-75`).
   - gotd returns `engine forcibly closed: context canceled` when a pooled connection dies mid-request, while the run's own context is still alive.
   - Once tdl's recovery retries run out (`--reconnect-timeout`, 5 minutes), the errgroup cancels every worker.
   - Every bar stays open, and `prog.Wait` loops forever with no output.
4. **A file is marked finished before it's renamed** (`:78-83`).
   - A failed rename, such as Windows' "used by another process" (iyear/tdl#1146), still records the file as finished.
   - A successful transfer whose byte count never reaches the bar's total leaves the bar open.

**Files:**
- Modify: `core/downloader/downloader.go` (`Download`)
- Create: `core/downloader/downloader_test.go`
- Modify: `app/dl/progress.go` (`OnDone`)
- Create: `app/dl/progress_test.go`

**Interfaces:**
- Unchanged signatures: `Download(ctx context.Context, limit int) error`, `OnDone(elem downloader.Elem, err error)`, and the `Iter`, `Elem`, `File` and `Progress` interfaces.
- Behaviour after this task:
  - `OnDone` receives the transfer's real error.
  - `Download` waits for its workers before returning, as `Uploader` does.
  - `OnDone` always closes the bar:
    - it marks it errored on any error, printing the red log line only when the error isn't a cancel;
    - it marks it done after successful post-processing, if the bar isn't done already.
  - go-pretty ignores "errored" on a bar that's already full. So a post-processing failure after a full transfer still shows `done!`, next to the red log line.
  - In every failure case, the file isn't recorded as finished. A file is recorded as finished only after its rename and `Chtimes` succeed.
  - On an iterator error, in-flight downloads run to completion and are recorded, as in `Uploader`. Ctrl+C still cancels them through the parent context.
- `core/forwarder` uses only `MaxPartSize`. `app/dl/progress.go` is the only `Progress` implementation.

- [ ] **Step 1: Write the failing downloader tests in `core/downloader/downloader_test.go`.** The test file uses `package downloader` and the standard `testing` package.
  - **Fakes:**
    - `fakeIter` yields the given elements. Before reporting the end, it waits for an optional `waitBeforeEnd` channel or `ctx.Done()`, and never calls `t` itself. `Err()` returns a configured error.
    - `fakeElem`:
      - `File()` returns DC 2, size `1 << 20`, and location `&tg.InputDocumentFileLocation{}`;
      - `To()` returns an in-memory `io.WriterAt`;
      - `AsTakeout()` returns false.
    - `fakeProgress` records every `OnDone` error under a mutex. `OnAdd` and `OnDownload` do nothing.
    - `fakePool` implements `dcpool.Pool`: `Client`, `Default` and `Takeout` return `tg.NewClient(inv)`, and `Close` returns nil.
    - Invokers:
      - `errInvoker` returns `tgerr.New(400, "CONNECTION_LAYER_INVALID")`. gotd v0.140.0's reader retries only flood wait and `TIMEOUT`, so the error comes back at once.
      - `releaseInvoker` closes `entered` once, then waits for `release` (or `ctx.Done()`) and returns `errStub`.
    - Build the downloader with `New(Options{Pool: pool, Threads: 1, Iter: iter, Progress: progress})`, using the fakes above.
  - **`TestDownloadReportsFailedTransfer`:**
    - One element, with `errInvoker`.
    - Assert `d.Download(ctx, 1)` returns nil.
    - Assert `OnDone` was called once, with an error where `tgerr.Is(err, "CONNECTION_LAYER_INVALID")`.
  - **`TestDownloadIterErrorWaitsForWorkers`:**
    - One element, with `releaseInvoker`. The iter's `waitBeforeEnd` is `inv.entered`, and `Err()` returns `errIter`.
    - Run `Download` in a goroutine.
    - On the test goroutine, wait for `inv.entered`, and `t.Fatal("worker never started")` if it takes more than 2 seconds.
    - Then, if `Download` returns within 100 ms, `t.Fatal("Download returned before its worker finished")`.
    - Then `close(release)`. It must return within 2 seconds.
    - Assert `errors.Is(err, errIter)`, and that `OnDone` was called once, with `errors.Is(err, errStub)`.
    - Cancel the test context in `t.Cleanup`, so a worker left blocked by the unfixed code exits.
- [ ] **Step 2: Write the failing progress tests in `app/dl/progress_test.go`.** The test file uses `package dl`.
  - **Setup:**
    - `p := newProgress(pw.NewWriter(), it, Options{})`, with `it := &iter{mu: &sync.Mutex{}, finished: map[int]struct{}{}}`.
    - The element is an `*iterElem` with:
      - `id: 1`, `logicalPos: 7`;
      - `to`: a file created at `<t.TempDir()>/a.bin.tmp`;
      - `file: &tmedia.Media{Name: "a.bin", Size: 1 << 20}`;
      - `fromMsg: &tg.Message{ID: 1}`;
      - `from`: a `fakePeer` that embeds `peers.Peer` and overrides only `ID()` and `VisibleName()`.
    - A helper builds a fresh `progress`, `iter`, element and temp file for every test and subtest. A shared file would make the second `OnDone` fail on "file already closed" and pass by accident.
    - Each test calls `p.OnAdd(elem)` first, and reads the tracker back with `p.trackers.Load(1)`.
  - **`TestOnDoneClosesBarOnError`:** subtests `canceled` (`context.Canceled`) and `failed` (`errors.New("boom")`). After `p.OnDone(elem, err)`, assert:
    - `tracker.IsDone()` and `tracker.IsErrored()`;
    - position 7 isn't in `it.Finished()`;
    - the `.tmp` file is gone.
  - **`TestOnDoneSuccessClosesBar`:** no `OnDownload` call, so the bar never reaches its total. After `p.OnDone(elem, nil)`, assert:
    - `tracker.IsDone()` and not `tracker.IsErrored()`;
    - position 7 is in `it.Finished()`;
    - `a.bin` exists.
  - **`TestOnDoneRenameFailureNotFinished`:**
    - Create a directory at `<dir>/a.bin` first, so the rename fails.
    - Call `p.OnDownload(elem, downloader.ProgressState{Downloaded: 1 << 20, Total: 1 << 20})`, so the bar is full, as in a real transfer.
    - After `p.OnDone(elem, nil)`, assert that position 7 isn't in `it.Finished()`.
- [ ] **Step 3: Run the tests and confirm they fail for the right reason.**
  - Run: `go test -race ./downloader/ -v` in `core`, and `go test -race ./app/dl/ -v` in `.`.
  - Expected to FAIL:
    - `TestDownloadReportsFailedTransfer`: `OnDone`'s error is nil;
    - `TestDownloadIterErrorWaitsForWorkers`: it returned before its worker finished;
    - `TestOnDoneClosesBarOnError/canceled`: the bar isn't done;
    - `TestOnDoneSuccessClosesBar`: the bar isn't done;
    - `TestOnDoneRenameFailureNotFinished`: position 7 is finished.
  - Expected to PASS: `TestOnDoneClosesBarOnError/failed`.
- [ ] **Step 4: Fix `Download`.**
  - Pass the `d.download` error to `OnDone` unchanged. Return it to the errgroup only when `errors.Is(err, context.Canceled)`, and only log other errors, as today.
  - Copy `Uploader`'s ending: `workerErr := wg.Wait()`, then return `errors.Wrap(err, "iter")` if `Iter.Err()` is set, otherwise `workerErr`.
- [ ] **Step 5: Fix `OnDone`.**
  - **On a non-nil error:** if `errors.Is(err, context.Canceled)`, call `t.MarkAsErrored()` with no log line; otherwise call `p.fail` as today. Remove the temp file, and return.
  - **On success:** run `donePost` first, and on an error call `p.fail` and return. Then call `p.it.Finish(e.logicalPos)`, and `if !t.IsDone() { t.MarkAsDone() }`.
    - The guard matters: a full bar is already done, and calling `MarkAsDone` again rewrites its stop time while the renderer reads it.
- [ ] **Step 6: Run the tests and confirm they pass.**
  - Run the two commands from Step 3.
  - Expected: all PASS.
- [ ] **Step 7: Lint and run the full tests.**
  - `golangci-lint run` in `.` and in `core`: expected no issues.
  - The tests in each module, per Global Constraints: expected to match the baseline, plus the new tests.
- [ ] **Step 8: Commit.** Message `fix(dl): report every download outcome and always close its progress bar`. The body refs iyear/tdl#1430 (the download half), iyear/tdl#1146 and abetoots/tdl#1.

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

- [ ] **Step 1: Record the module sets before the bump**, saved to the scratchpad. Run from the repo root, in workspace mode, and stop if any command fails:
  - **All selected modules:** `go list -m -f '{{.Path}}@{{.Version}}' all | sort > mods-before.txt`.
  - **Modules compiled into the two deliverables:** `for os in linux windows; do CGO_ENABLED=0 GOOS=$os GOARCH=amd64 go list -deps -f '{{with .Module}}{{.Path}}@{{.Version}}{{end}}' . > compiled-$os.txt || exit 1; done; sort -u compiled-linux.txt compiled-windows.txt > compiled-before.txt`.
- [ ] **Step 2: Fix the `Logger` field.** In `core/tclient/tclient.go`, use `Logger: logzap.New(logctx.From(ctx).Named("td"))` and import `github.com/gotd/log/logzap`.
- [ ] **Step 3: Resolve the new versions.** These commands ignore `go.work`, so set `GOWORK=off` explicitly.
  - In `core`: `GOWORK=off go get github.com/gotd/td@v0.166.1 github.com/gotd/log/logzap@v0.1.1`.
  - Then in `extension`, and then in `.`: `GOWORK=off go get github.com/gotd/td@v0.166.1`.
  - Then `GOWORK=off go mod tidy` in all three.
  - Expected:
    - all three `go.mod` files require `github.com/gotd/td v0.166.1`;
    - `core/go.mod` requires `github.com/gotd/log/logzap v0.1.1` directly, and `github.com/gotd/log v0.1.0` as `// indirect`;
    - root and `extension` may list `github.com/gotd/log` as `// indirect`;
    - `github.com/gotd/ige` moves to v0.3.0;
    - versions raised because gotd/td v0.166.1's `go.mod` requires them: coder/websocket, ogen, otel, go-logr, go-colorable, go-isatty, regexp2;
    - new: `refraction-networking/utls v1.8.2` and `andybalholm/brotli`, compiled in through `telegram/dcs` → `mtproxy/faketls`;
    - `gotd/contrib` stays at v0.20.0;
    - any other change matches Task 0's `tidy -diff` drift, or is explained in Step 5.
- [ ] **Step 4: Build, vet, test and lint, in workspace mode.**
  - In each of `.`, `core` and `extension`: `go build ./...`, `go vet ./...`, the tests per Global Constraints, and `golangci-lint run`.
  - Expected: tests and lint match the baseline.
  - **If any file other than `core/tclient/tclient.go` fails to compile, stop and report the errors.** That file is outside this round's paths.
- [ ] **Step 5: Supply-chain review.** Record the after sets the same way as Step 1.
  - **The review surface** is the compiled-in diff, `compiled-before.txt` against `compiled-after.txt`.
  - For each new or moved compiled-in `path@version`:
    - **Requirer:** the two pins this step adds, `github.com/gotd/td@v0.166.1` and `github.com/gotd/log/logzap@v0.1.1`, skip this check, since tdl's own modules require them. Every other version is checked with `for d in . core extension; do (cd $d && GOWORK=off go mod graph); done | awk '$2 == "<path>@<version>"'`, which must name one of the two pins, or a module that one of them requires.
    - **Age:** find publication evidence for each version: its GitHub release or tag date (for `golang.org/x` and `go.opentelemetry.io`, their GitHub mirrors). `go list -m -json … .Time` alone isn't enough, because it can be an old commit date on a new tag.
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
- [ ] **Step 3: Run into a fresh directory:** `tdl-smoke dl -f <export>.json --takeout -d <new-empty-dir> --restart`.
  - Use a new directory, so every file in it was written by the fixed build.
  - Leave out `--debug`. The default Info level already logs the strings below, and debug output rotates `latest.log` sooner (10 MB, 3 gzip backups).
  - Let it run at least as long as the failed run did before it aborted, or to the end.
  - **Pass:**
    - it ends by itself, with no `CONNECTION_LAYER_INVALID` abort. If you stop it with Ctrl+C, it exits within a few seconds;
    - at least one file was downloaded, and its size matches the size Telegram shows for it;
    - any failed file shows as `failed!`, not `done!`.
  - **Record:**
    - whether `Server lost connection state, re-sending initConnection` and `Connection re-initialized` appear, and whether downloads went on afterwards. Search `latest.log` and the rotated `.gz` files next to it (`zgrep` on WSL);
    - any failed files.
  - If takeout can't start, the log shows `takeout error` and the downloads go on without takeout.
- [ ] **Step 4: If no file starts and no byte count moves for 2 minutes,** capture what it's doing before stopping it:
  - **On WSL:** run `kill -QUIT <pid>`. The Go runtime prints every goroutine's stack and exits.
  - **On Windows:** rerun with `--debug`.
  - Attach the output or a `latest.log` excerpt to abetoots/tdl#1, with dialog IDs, message IDs and usernames redacted.
- [ ] **Step 5: Repair the original download directory.**
  - The old build renamed failed transfers to their final names. With several threads, a failed file can still be full size, with a hole in it. `--skip-same` compares only name and size, so it can't spot those.
  - **If Step 3 ran to completion,** the new directory is a clean, complete copy. Use it in place of the original.
  - **Otherwise,** find the files the failed run created by creation time, not modification time (tdl sets that to the message date).
    - On Windows: `Get-ChildItem <dir> -Recurse -File | Where-Object CreationTime -ge '<failed run start>'` in PowerShell.
    - On WSL, `stat -c %w` prints `-` under `/mnt/c`, so use it only for directories inside WSL.
    - Delete those files, then run `tdl-smoke dl -f <export>.json --takeout -d <original-dir> --restart --skip-same`. It re-downloads them and skips the rest.
  - `--skip-same` can't match files whose extension an earlier `--rewrite-ext` run changed. The original command didn't use it.

## Owner decisions (2026-11-26)

- Tracking may publish to this public fork.
- Pull requests stay on the fork; nothing goes upstream.
- The goal is a working binary, for WSL or for Windows.
- The plan gets multi-model sign-off before any work starts.
- Codex, on its default model, implements; a `/gauntlet` review runs at the end.

## Deferred (not in this plan)

- gotd/contrib v0.20.0 → v0.26.0 (needs td ≥ v0.164.0; floodwait gains concurrent dispatch): a separate pull request.
- CI's unit-test step covers only the root module's packages. `core` and `extension` tests don't run in CI.
- **On Ctrl+C,** `prog.Wait` returns without stopping the renderer. This is cosmetic.
- **go-pretty ignores `MarkAsErrored` on a bar that's already at 100%.** A close or rename failure after a full transfer still shows `done!`, although the red error line is printed. This is a display issue only.
- **A successful transfer's byte count isn't checked** against the file size.
- 32-bit: upstreaming waits for gotd#1985. Other `go.uber.org/atomic.Int64` fields in gotd are unverified.
- `recovery` retries `context canceled` for up to `--reconnect-timeout`, or forever with `0` (iyear/tdl#854, #865). Needs a redesign.
- Retest after the bump: iyear/tdl#1071, #900, #876, #385 and #1147. #1363 is fixed by 77cb441.
- Show the takeout fallback in the terminal, not only in the log.
- iyear/tdl#1430, the upload half; iyear/tdl#1112, `chat ls` panics; gotd#1980, `dialogCommunity`.
- Watch `dl serve` memory with the unbounded peers cache (#1962).
