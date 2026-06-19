# Correction Plan for Thermos Review Findings

## Goal

Address every finding from the thermos review of the `v2` refactor (removing `github.com/pkg/errors` and `github.com/gofrs/flock`) so the branch is safe to merge. Changes are limited to the issues raised; no opportunistic refactoring outside this scope.

## Decisions

| #   | Decision                                           | Chosen Option                                                                                                                | Rationale                                                                                                                                          |
| --- | -------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------ | -------------------------------------------------------------------- |
| 1   | Order of fixes                                     | 1) stack-renderer bug, 2) CI cross-compile scope, 3) load-failure cleanup, 4) cross-platform tests, 5) low-risk polish       | No runtime defect to fix in `TryLock` once behavior matches upstream; correctness/CI issues come first, then polish.                               |
| 2   | `Flock.TryLock` already-locked behavior            | Keep short-circuit returning `(true, nil)`, but document it explicitly                                                       | Matches the new test `TestTryLockAlreadyLocked`; `dque` uses one `Flock` under a mutex, so re-acquiring the kernel lock adds no value.             |
| 3   | `Flock.TryLock` file-opened-but-not-locked cleanup | Match `gofrs/flock`: leave `f.file` open on failure and document that callers must use `Unlock`/`Close` to release resources | Preserves behavioral compatibility with the dependency being replaced; `os.File` finalizers prevent permanent leaks.                               |
| 4   | Stack-trace rendering                              | Process the frame returned by `frames.Next()` before checking `more`                                                         | Fixes the dropped-last-frame bug while preserving the existing `Error()` format.                                                                   |
| 5   | `New`/`Open` load-failure cleanup                  | Call `q.fileLock.Close()`; return the load error as primary, attaching close/unlock error with `errors.Join`                 | Closes the descriptor and preserves the root cause. Uses `errors.Join` (Go 1.20+) instead of `fmt.Errorf` to avoid adding a formatting dependency. |
| 6   | Cross-platform invalid-directory tests             | Replace `"/nonexistent/dir"` with `filepath.Join(t.TempDir(), "does-not-exist")`                                             | Avoids Unix-absolute paths and works on Windows/Darwin.                                                                                            |
| 7   | Read-only-directory segment test                   | Skip the `0500` sub-case on Windows or when running as root (`runtime.GOOS == "windows"                                      |                                                                                                                                                    | os.Getuid() == 0`) | Prevents false positives on platforms where permissions are ignored. |
| 8   | Corrupted-segment path assertion                   | No change                                                                                                                    | `segment.go` uses `path.Join`, which always produces `/` separators, so the existing assertion is already cross-platform.                          |
| 9   | CI cross-compile steps                             | Change `go test -c` to `go test -c ./...` for Windows and Darwin                                                             | Ensures internal package test files are compiled on all target platforms.                                                                          |
| 10  | `DQue.Close` partial-close state                   | Clean up both segments even if one close fails, then return the first error                                                  | Avoids hiding segment-close errors behind `ErrQueueClosed` on retry.                                                                               |
| 11  | Stack-trace noise in `Error()`                     | Accept as designed per the original refactor plan                                                                            | Plan intentionally renders traces in `Error()`; future work could expose `%+v` separately.                                                         |
| 12  | Test repetition                                    | Add small helpers/table-driven tests where it reduces duplication without large rewrites                                     | Addresses the code-quality feedback while keeping the change minimal.                                                                              |

## Issues and Corrective Actions

### 1. Accepted — File-descriptor behavior in `internal/flock/flock.go` matches `gofrs/flock`

- **File:** `internal/flock/flock.go`
- **Context:** The thermos review flagged that `TryLock` leaves the underlying file descriptor open when it returns `(false, nil)` for an already-locked file. `github.com/gofrs/flock` exhibits the same behavior: `setFh()` opens the file, `try()` returns `(false, nil)` on `EWOULDBLOCK` / `ERROR_LOCK_VIOLATION` without closing `f.fh`, and the descriptor is only closed by `Unlock()` / `Close()`.
- **Change:** Do **not** close the file in `TryLock()` on failure. Instead, document clearly in the `TryLock` doc comment that a failed acquisition leaves the file handle open and that callers must call `Unlock()` or `Close()` to release resources.
- **Test:** Do not add a test asserting the file is closed after a failed `TryLock`; keep the existing tests that verify the lock/unlock lifecycle.

### 2. Medium — Stack-trace renderer drops the last frame

- **File:** `internal/errors/errors.go`
- **Change:** Rewrite `renderStack` so the frame returned by `frames.Next()` is rendered before checking `more`.
- **Test:** Add a regression test in `internal/errors/errors_test.go` asserting that a wrapped error contains at least the expected test frame and that the rendered stack is non-empty end-to-end.

### 3. Medium — CI cross-compile steps miss internal tests

- **File:** `.github/workflows/test.yaml`
- **Change:** Update the `Test building on windows` and `Test building on darwin` steps from `go test -c` to `go test -c ./...`. Consider making the root `Build tests` step use `go test -c ./...` as well for consistency.

### 4. Medium — `queue.go` load-failure path leaks lock file and masks root error

- **File:** `queue.go`
- **Change:** In `New()` and `Open()`, on `q.load()` failure call `q.fileLock.Close()` (which unlocks and closes). Return the load error as the primary error; if releasing the lock file also fails, use `errors.Join(loadErr, releaseErr)` so both errors are preserved.
- **Dependency:** Requires adding `errors` to `queue.go` imports.

### 5. Medium — Cross-platform test fragility

- **File:** `queue_test.go`
- **Change:** Replace `"/nonexistent/dir"` in `TestQueue_NewValidationErrors`, `TestQueue_OpenValidationErrors`, and `TestQueue_NewOrOpenValidationErrors` with `filepath.Join(t.TempDir(), "does-not-exist")`.

- **File:** `segment_test.go`
- **Change:** In `TestSegment_newQueueSegmentErrors`, guard the `os.Mkdir(readOnlyDir, 0500)` sub-case with a skip when `runtime.GOOS == "windows"` or `os.Getuid() == 0`.

### 6. Low — `DQue.Close` can hide segment close errors

- **File:** `queue.go`
- **Change:** In `Close()`, attempt to close both segment files, nil them out, and clear `fileLock` even if one close returns an error. Return the first error encountered.

### 7. Low — Test duplication and boilerplate

- **File:** `queue_test.go`
- **Change:** Convert `TestQueue_NewValidationErrors`, `TestQueue_OpenValidationErrors`, and `TestQueue_NewOrOpenValidationErrors` into a single table-driven test parameterized by constructor function.

- **File:** `segment_test.go`
- **Change:** Replace the manual `os.RemoveAll` / `os.Mkdir` / `defer os.RemoveAll` setup in the new segment error tests with `t.TempDir()`. For tests that assert on the segment path, construct the expected path from the temporary directory instead of hard-coding the directory name.

### 8. Low — Documentation / style nits

- **File:** `internal/flock/flock.go`
- **Change:** Update the `TryLock` doc comment to document that calling it on an already-locked `Flock` returns `(true, nil)`.

- **File:** `segment.go`
- **Change:** Fix typo: `know` → `known` in the `turboOff` comment.

- **File:** `segment_test.go`
- **Change:** Remove superfluous inner parentheses in `os.RemoveAll((testDir))` and the extra blank line around line 130–131.

## Implementation Order

1. Fix `internal/errors/errors.go` `renderStack` loop and add a regression test.
2. Document `internal/flock/flock.go` `TryLock` resource-ownership semantics to match `gofrs/flock`.
3. Update `queue.go` load-failure cleanup and `Close` cleanup.
4. Fix cross-platform test fragility in `queue_test.go` and `segment_test.go`.
5. Refactor duplicated test setup in `queue_test.go` and `segment_test.go`.
6. Update `.github/workflows/test.yaml` cross-compile steps.
7. Apply documentation and style nits.
8. Run full verification suite.

## Verification

- `go test -race -cover ./...` must pass.
- `go vet ./...` must pass.
- Cross-compile checks must pass:
  - `GOOS=windows go test -c ./...`
  - `GOOS=darwin go test -c ./...`
  - `GOOS=linux go test -c ./...`
- Coverage for `internal/errors` and `internal/flock` should remain at or near 100%.

## Risks

- Leaving `Flock.TryLock` file handles open on failure matches `gofrs/flock` but relies on callers to `Close()`/`Unlock()`; document this contract clearly.
- Returning the load error as primary (with `errors.Join`) changes the error string/type seen by callers when both load and lock-release fail. This is considered an improvement.
- `DQue.Close` cleanup changes error semantics slightly; callers already cannot rely on a half-closed queue.
- Table-driving validation tests is a small refactor; verify each constructor still exercises its unique path (`Open` requires the queue to exist).

## Iteration Log

- **Iteration 1:** Drafted from the thermos review synthesis.
- **Iteration 2:** Switched load-failure error handling to `errors.Join` to avoid new `fmt` import; clarified FD-leak test strategy; added test-duplication cleanup items; added dependency/import notes.
- **Iteration 3:** Refined FD-leak fix to also close the file on syscall error after open; noted optional consistency update for the root `Build tests` step.
- **Iteration 4:** Reverted the FD-leak fix after confirming that `github.com/gofrs/flock` leaves the file handle open on failed `TryLock`; replaced it with a documentation requirement to match upstream behavior.
- **Iteration 5:** Resolved remaining grill branches with user input: use `t.TempDir()` for segment test setup, nil queue state on `Close` error, `runtime.goexit` regression test. No further substantial changes; plan converged.
