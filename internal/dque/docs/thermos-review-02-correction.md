# Correction Plan for Thermos Review 02 Findings

## Goal

Address every finding in `thermos-review-02.md` so the `v2` branch is safe to merge. The scope is the `v2` refactor that removes `github.com/pkg/errors` and `github.com/gofrs/flock`, adds `internal/errors` + `internal/flock`, bumps Go to `1.26.0`, and modernizes CI/tests.

Changes are limited to the issues raised; opportunistic refactoring outside this scope is avoided unless it is required to fix a finding.

## Decisions

| #   | Decision                                   | Chosen Option                                                                                                                                                                                        | Rationale                                                                                                                                                                                                                                         |
| --- | ------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | Order of fixes                             | 1) blockers (FD leak, gofmt, lint.yaml, portability), 2) medium-priority correctness/races, 3) low-priority polish, 4) documentation                                                                 | Blockers prevent merge; correctness issues come next; polish is last.                                                                                                                                                                             |
| 2   | `TryLock` file-handle semantics            | Keep the current behavior that matches `gofrs/flock` (file stays open on a failed acquisition), but fix the caller in `queue.lock()` to always close the `Flock` when acquisition fails              | Avoids changing the internal `Flock` contract while eliminating the real leak in `dque`.                                                                                                                                                          |
| 3   | `Flock.Close` unlock failure               | Always close the underlying `os.File` even if `unlock()` fails; join the two errors with `errors.Join`                                                                                               | Prevents a file-handle leak on unlock failure.                                                                                                                                                                                                    |
| 4   | Unsupported platforms for `internal/flock` | Add a `!unix && !windows` build-tagged stub that returns an error from `tryLock()` and `unlock()`                                                                                                    | Keeps the package compiling on `plan9`, `js/wasm`, etc., while clearly failing at runtime. Document the limitation.                                                                                                                               |
| 5   | Stack traces in `Error()`                  | Keep rendering stack traces in `Error()` as a deliberate design choice, but document the behavioral change prominently in the README/package doc                                                     | Matches the original refactor plan; callers doing string equality on wrapped errors need warning.                                                                                                                                                 |
| 6   | Data races on closed-state checks          | Introduce an explicit `closed` boolean protected by `q.mutex`, set it in `Close()`, and read it under the lock (or before releasing the lock) in `Size`, `SizeUnsafe`, `SegmentNumbers`, and `Turbo` | Eliminates the race between `Close()` nil-ing `fileLock` and unsynchronized readers. `Turbo()` can simply return the `turbo` field but still needs to avoid observing torn state if desired; the minimal fix is to check `closed` under the lock. |
| 7   | `path.Join` vs `filepath.Join`             | Replace all filesystem-path construction in `queue.go` and `segment.go` with `filepath.Join`                                                                                                         | Fixes the pre-existing portability wart. Corrupted-segment test assertions must be updated to use `filepath.Join` or `filepath.FromSlash` if they currently assert on slash-separated strings.                                                    |
| 8   | Path traversal via queue `name`            | Validate `name` in `New`/`Open`/`NewOrOpen`: reject empty names, path separators, and `..` segments                                                                                                  | Closes the security issue without breaking legitimate queue names.                                                                                                                                                                                |
| 9   | Hermetic tests                             | Migrate legacy tests to `t.TempDir()` and queue names relative to that directory; remove manual `os.RemoveAll` cleanup where possible                                                                | Reduces cross-contamination and makes tests hermetic.                                                                                                                                                                                             |
| 10  | `itemsPerSegment` validation               | Require `itemsPerSegment > 0` in all constructors; return a clear error otherwise                                                                                                                    | Prevents unbounded segment creation.                                                                                                                                                                                                              |
| 11  | `load()` all-segments-empty edge case      | After the deletion loop, if `minNum > maxNum`, fall back to creating segment 1 instead of calling `openQueueSegment` with an invalid number                                                          | Handles corruption/manual manipulation gracefully.                                                                                                                                                                                                |
| 12  | Error comparison in tests                  | Replace `err.Error() == "..."` checks with `errors.Is(err, sentinel)` or sentinel identity where appropriate                                                                                         | Makes tests robust against formatting changes.                                                                                                                                                                                                    |

## Issues and Corrective Actions

### Blockers

#### 1. File-descriptor leak when `TryLock` fails

- **Files:** `queue.go:577–590`, `internal/flock/flock.go:26–47`
- **Finding:** `lock()` creates a `Flock`, calls `TryLock()`, and on `(false, nil)` or `(false, err)` returns without `Close()`. `TryLock()` deliberately leaves the file open on failure to match `gofrs/flock`, but the caller is not cleaning up.
- **Change:** In `queue.go` `lock()`, defer a `fileLock.Close()` that is canceled only after the lock is successfully assigned to `q.fileLock`. Concretely:
  - Create the `Flock`.
  - `defer` a close call that checks whether `q.fileLock == fileLock`; if not, close it.
  - Call `TryLock()`.
  - On error or `!locked`, return the appropriate error (the deferred close releases the descriptor).
  - On success, assign `q.fileLock = fileLock` so the deferred close becomes a no-op.
- **Test:** Add a regression test (where feasible) that creates two queues for the same path and asserts no descriptor leak on the second attempt, or at minimum exercise the conflict path under `-race`.

#### 2. Code is not `gofmt`-clean

- **Files:** `queue.go`, `benchmark_test.go`
- **Finding:** `gofmt -l .` reports both files.
- **Change:** Run `gofmt -w queue.go benchmark_test.go`.
- **Verification:** Re-run `gofmt -l .` and ensure it prints nothing.

#### 3. Lint CI workflow is incompatible with Go 1.26

- **File:** `.github/workflows/lint.yaml`
- **Finding:** Uses `actions/checkout@v1` and `golangci-lint v1.22.2` (2020); that version cannot parse Go 1.26 source.
- **Change:** Rewrite `.github/workflows/lint.yaml` to mirror `test.yaml`:
  - Use `actions/checkout@v4`.
  - Use `actions/setup-go@v5` with Go `1.26.0`.
  - Use the official `golangci-lint-action@v6` with a current golangci-lint version (e.g., `v1.59+` or `latest`) or, if project policy prefers a pinned version, a version known to support Go 1.26.
  - Run `golangci-lint run ./...`.
- **Verification:** The workflow should succeed on push/PR.

#### 4. `internal/flock` does not build on `plan9`, `js/wasm`, etc.

- **Files:** `internal/flock/flock_unix.go`, `internal/flock/flock_windows.go`
- **Finding:** `GOOS=plan9 go test -c ./internal/flock` and `GOOS=js GOARCH=wasm …` fail with undefined `tryLock`/`unlock`.
- **Change:** Add `internal/flock/flock_stub.go` with build tag `//go:build !unix && !windows` (or an explicit list of unsupported targets). Implement:
  ```go
  func (f *Flock) tryLock() (bool, error) { return false, errors.New("flock: unsupported platform") }
  func (f *Flock) unlock() error { return errors.New("flock: unsupported platform") }
  ```
  Add the `errors` import to the stub file.
- **Documentation:** Add a note to `internal/flock/flock.go` package doc or README stating that only Unix-like systems and Windows are supported at runtime; other targets compile but will fail at lock acquisition.
- **Verification:** Cross-compile checks pass for `plan9`, `js/wasm`, `linux`, `windows`, `darwin`.

### Medium Priority

#### 5. Stack traces rendered inside `Error()`

- **File:** `internal/errors/errors.go:52–59`
- **Finding:** By-design behavioral change that breaks callers doing `err.Error() == "..."` on wrapped errors.
- **Change:** Keep the current `Error()` behavior. Add prominent documentation:
  - In `internal/errors/errors.go` package doc comment.
  - In `README.md` (v2 migration section) noting that wrapped errors now include stack traces in `Error()` and that `errors.Is`/`errors.As` should be used instead of string equality.
- **Test:** No code change required unless a test currently asserts exact `Error()` strings for wrapped errors; update those tests to use `errors.Is`.

#### 6. `Flock.Close` leaks file handle if unlock fails

- **File:** `internal/flock/flock.go:62–79`
- **Finding:** Returns early on unlock error and never reaches `f.file.Close()`.
- **Change:** Rewrite `Close()` so it always closes `f.file` (if non-nil) and joins any unlock error with the close error:
  ```go
  func (f *Flock) Close() error {
      if f.file == nil {
          return nil
      }
      var errs []error
      if f.locked {
          if err := f.unlock(); err != nil {
              errs = append(errs, err)
          }
          f.locked = false
      }
      if err := f.file.Close(); err != nil {
          errs = append(errs, err)
      }
      f.file = nil
      return errors.Join(errs...)
  }
  ```
  Add `errors` to the imports in `internal/flock/flock.go`.
- **Test:** Add a test (using a stub or platform-specific failure injection if possible) that asserts `Close` closes the file even when unlock returns an error. At minimum add a test that a normal `Close` nils `f.file`.

#### 7. Data races on closed-state checks

- **File:** `queue.go` (`Size`, `SizeUnsafe`, `SegmentNumbers`, `Turbo`)
- **Finding:** These methods read `q.fileLock` / `q.turbo` outside the mutex while `Close()` writes `q.fileLock = nil` under the lock.
- **Change:**
  - Add `closed bool` to `DQue`.
  - In `Close()`, set `q.closed = true` while holding `q.mutex`.
  - Update `Size()`, `SizeUnsafe()`, `SegmentNumbers()`, and `Turbo()` to acquire `q.mutex` and check `q.closed` before reading `q.fileLock`, `q.firstSegment`, `q.lastSegment`, or `q.turbo`. For `SizeUnsafe()`, keep its documented best-effort semantics but eliminate the data race with `Close()` by reading `closed` under the lock; the actual size calculation can still be unsynchronized as documented.
- **Test:** Run `go test -race -count=5 ./...` and confirm no race reports from these methods.

#### 8. `path.Join` used for filesystem paths

- **Files:** `queue.go`, `segment.go`
- **Finding:** Pre-existing portability wart.
- **Change:** Replace `path.Join` with `filepath.Join` for all filesystem-path construction, including the lock file path in `queue.lock()` (`queue.go:578`). Audit `queue.go`, `segment.go`, and `util.go` for any remaining `path.Join` used for actual disk paths. Tests that assert on constructed paths must use `filepath.Join`/`filepath.FromSlash` or compare via `filepath.Clean`.
- **Verification:** Cross-compile and run tests on Windows if possible; at minimum ensure `filepath` is imported.

#### 9. Path traversal via queue `name`

- **File:** `queue.go:70–86`
- **Finding:** `name` is only checked for emptiness; `../` segments allow creation outside `dirPath`.
- **Change:** Add a validation helper used by `New`, `Open`, and `NewOrOpen` that rejects `name` if it:
  - is empty,
  - contains `os.PathSeparator` or `'/'` or `'\\'` (after normalization),
  - is `.` or `..`,
  - contains any `..` segment when split by separators.
- **Test:** Add tests for `../queue`, `queue/../other`, and absolute paths.

#### 10. Tests are not hermetic

- **Files:** `queue_test.go`, `segment_test.go`
- **Finding:** Many tests use hardcoded names like `"test1"`, `"./TestSegment"` and manual `os.RemoveAll`.
- **Change:**
  - Refactor helpers `newQ`, `openQ`, `newOrOpenQ` (and any segment helpers) to accept a `*testing.T` and create queues under `t.TempDir()`.
  - Replace hardcoded queue/segment directory names with unique subdirectories of `t.TempDir()`.
  - Remove manual `os.RemoveAll` calls where `t.TempDir()` now handles cleanup.
- **Scope note:** This is intentionally scoped to the tests that are touched by the refactor or that demonstrate flakiness; fully rewriting every legacy test is not required, but the plan should migrate the heavily duplicated/conflicting ones (`test1`, `TestSegment`, `TestQueue_BlockingAggresive`, `TestQueue_Add9Remove8`). The new tests already use `t.TempDir()`; this item focuses on legacy tests.

#### 11. `itemsPerSegment` not validated

- **File:** `queue.go` constructors
- **Finding:** Zero/negative values cause unbounded segment creation.
- **Change:** In the shared validation helper (see #9), also require `itemsPerSegment > 0`. Return a clear error such as `"itemsPerSegment must be greater than zero"`.
- **Test:** Add a table-driven test covering `0` and negative values for `New`, `Open`, and `NewOrOpen`.

#### 12. `load()` edge case: all segments empty+complete

- **File:** `queue.go:531–547`
- **Finding:** If every on-disk segment is empty and complete, `minNum` increments past `maxNum` and `openQueueSegment` fails.
- **Change:** After the deletion loop, if `minNum > maxNum`, treat it as "no usable segments found" and fall back to creating segment 1 (the same branch used when `maxNum == 0`).
- **Test:** Add a test that constructs a queue directory with only empty-complete segments, opens it, and asserts segment 1 exists and the queue is usable.

### Low Priority / Polish

#### L1. `TestQueue_UseAfterClose` compares `err.Error()` strings

- **File:** `queue_test.go`
- **Change:** Replace string comparison with `errors.Is(err, dque.ErrQueueClosed)` (or direct sentinel comparison).

#### L2. Duplicated `assert` helper across `queue_test.go` and `segment_test.go`

- **Files:** `queue_test.go`, `segment_test.go`
- **Change:** Move the `assert` helper to a shared `testing_test.go` file (package `dque_test`) or create a small internal test-utility package. Update all call sites.

#### L3. Duplicated constructor validation in `New`/`Open`/`NewOrOpen`

- **File:** `queue.go`
- **Change:** Extract the validation logic into an unexported helper, e.g. `func validateQueueInputs(name, dirPath string, itemsPerSegment int) (string, error)` that returns `fullPath` or an error. Call it from all three constructors.

#### L4. Typos

- **File:** `segment.go:316`
- **Change:** Fix `ssegments` → `segments`. Fix any other grammar nits discovered while editing (e.g., "Speed is be greatly increased" in `turboOn` comment).

#### L5. Public API changes in v2 not documented

- **File:** `CHANGELOG.md` (new)
- **Change:** Create a `CHANGELOG.md` documenting v2 breaking changes:
  - `Config` → `config` (unexported).
  - `DQUE_EMPTY` → `ErrEmpty`.
  - New error package behavior (stack traces in `Error()`).
  - Removal of `github.com/pkg/errors` and `github.com/gofrs/flock`.
  - Go version bump to `1.26.0`.

#### L6. Windows `LockFileEx` implementation only cross-compiled

- **File:** `.github/workflows/test.yaml`
- **Change:** Add a Windows runner step that runs `go test -race -cover ./...` on `windows-latest` (in addition to the existing cross-compile steps). If Windows tests are not feasible in the current CI budget, document this limitation in `CHANGELOG.md` or CI comments.

#### L7. `segment.close()` does not nil out `seg.file` after closing

- **File:** `segment.go`
- **Change:** In `segment.close()`, set `seg.file = nil` after a successful close (and also on error if the handle is known to be invalid).
- **Test:** Add a test asserting `seg.file` is nil after close.

### Uncertainty / Could Not Fully Confirm

#### U1. Flaky `TestQueue_BlockingAggresive`

- **File:** `queue_test.go`
- **Finding:** Intermittent `-race` failures (timeout + "file already exists").
- **Change:** Switch the test to use `t.TempDir()` and a unique queue name. If failures persist after hermeticization and race fixes (#7), investigate the goroutine choreography; do not block the merge on an unconfirmed flake.

#### U2. `TestQueue_Add9Remove8` stale segment numbers

- **File:** `queue_test.go`
- **Finding:** Post-reopen assertions use pre-close variables because the reopened queue is assigned to `_`.
- **Change:** Assign the reopened queue to a new variable and re-read `SegmentNumbers()` after reopening; update assertions accordingly.

## Implementation Order

1. Run `gofmt -w queue.go benchmark_test.go` (#2).
2. Update `.github/workflows/lint.yaml` (#3).
3. Add unsupported-platform stub to `internal/flock` (#4).
4. Fix `Flock.Close` to always close the file and join errors (#6).
5. Fix `queue.go` `lock()` to close the `Flock` on failed `TryLock` (#1).
6. Extract shared constructor validation helper; add `name` path-traversal check and `itemsPerSegment > 0` check (#9, #11, L3).
7. Fix `load()` all-empty-complete edge case (#12).
8. Replace `path.Join` with `filepath.Join` in `queue.go` and `segment.go`; fix `segment.close()` to nil `seg.file` (#8, L7).
9. Address data races on closed-state checks (#7).
10. Document stack-trace behavior and create `CHANGELOG.md` (#5, L5).
11. Refactor tests for hermeticity: use `t.TempDir()`, fix `TestQueue_Add9Remove8`, migrate flaky `TestQueue_BlockingAggresive` (#10, U2, U1).
12. Replace `err.Error()` comparisons with `errors.Is` where applicable (L1).
13. Deduplicate `assert` helper (L2).
14. Fix typos (L4).
15. Add Windows test runner if feasible (L6).
16. Run full verification suite.

## Verification

- `gofmt -l .` returns no files.
- `go vet ./...` passes.
- `go test -race -cover ./...` passes on Linux.
- Cross-compile checks pass:
  - `GOOS=windows go test -c ./...`
  - `GOOS=darwin go test -c ./...`
  - `GOOS=linux go test -c ./...`
  - `GOOS=plan9 go test -c ./...`
  - `GOOS=js GOARCH=wasm go test -c ./...`
- Coverage for `internal/errors` and `internal/flock` remains at or near 100%.
- `golangci-lint run ./...` (via updated CI) passes.
- Race detector runs clean on the methods identified in #7.

## Risks

- Changing `Error()` output is a documented behavioral change; downstream string equality checks may break.
- Adding path-traversal validation could reject previously accepted (but unsafe) queue names.
- `Flock.Close` now returns joined errors instead of a single unlock error; callers should use `errors.Is` if they inspect the error.
- Migrating tests to `t.TempDir()` may expose latent ordering assumptions between tests.
- Adding a Windows CI runner may surface Windows-specific failures not caught by cross-compilation.

## Iteration Log

- **Iteration 1:** Initial draft created from `thermos-review-02.md` findings.
- **Iteration 2:** Reviewed the draft against the review findings. Refined the data-race fix to keep `SizeUnsafe()`'s documented best-effort semantics while eliminating races with `Close()`. Explicitly called out the lock-file path (`queue.go:578`) and noted that legacy tests are the focus of the hermeticity work.
- **Iteration 3:** Re-reviewed all blockers, medium, low, and uncertainty items against `thermos-review-02.md`. Confirmed every finding has a corresponding corrective action and verification step. No significant gaps identified; plan converged.
- **Iteration 4:** Final review. Verified the duplicated `assert` helper exists in both test files, confirmed `path.Join` locations, and checked that `SizeUnsafe()` and `Turbo()` race descriptions align with the review. No further changes required.
- **Iteration 5:** Independent coverage audit by an explore agent confirmed all 21 findings from `thermos-review-02.md` map to concrete corrective actions in this plan. No significant gaps remain. Plan is converged and ready for execution.
