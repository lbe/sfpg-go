# Plan: Remove pkg/errors and gofrs/flock Third-Party Dependencies

## Goal

Make `github.com/lbe/sfpg-go` depend only on the Go standard library by replacing `github.com/pkg/errors` and `github.com/gofrs/flock` with small internal packages.

## Decisions

| #   | Decision                        | Chosen Option                                                                                      |
| --- | ------------------------------- | -------------------------------------------------------------------------------------------------- |
| 1   | Minimum Go version              | `1.26.0`                                                                                           |
| 2   | pkg/errors replacement          | Internal helper package that preserves stack traces                                                |
| 3   | Helper package location and API | `internal/errors` exporting `New`, `Wrap`, `Wrapf`                                                 |
| 4   | Stack-trace capture             | Only `Wrap`/`Wrapf` capture stack traces; `New` returns plain stdlib errors                        |
| 5   | Stack-trace access              | Only rendered in `Error()` string; no programmatic `StackTrace()` API                              |
| 6   | Error string format             | The `"msg: cause"` portion is preserved exactly; the stack trace is appended after it in `Error()` |
| 7   | gofrs/flock replacement         | `internal/flock` with platform-specific files                                                      |
| 8   | Windows locking strategy        | Use `syscall.LockFileEx` from stdlib `syscall` package                                             |
| 9   | Flock API scope                 | Minimal: `New`, `TryLock`, `Unlock`, `Close`                                                       |
| 10  | Legacy documentation            | `internal/flock/doc.go` noting `gofrs/flock` legacy and unimplemented features                     |
| 11  | Testing                         | Unit tests for both `internal/errors` and `internal/flock`                                         |
| 12  | Scope                           | Strictly focused on dependency removal; no opportunistic modernizations                            |

## Files to Create

1. `internal/errors/errors.go`
   - `New(msg string) error` — delegates to stdlib `errors.New`; no stack trace.
   - `Wrap(err error, msg string) error` — if `err` is `nil`, returns `nil` (matches `pkg/errors.Wrap` behavior); otherwise returns `"msg: cause"` with a captured stack trace.
   - `Wrapf(err error, format string, args ...interface{}) error` — same as `Wrap` but with formatted message.
   - Internal `wrappedError` type implementing `Error()` and `Unwrap()`.
   - `Wrap`/`Wrapf` capture a stack trace via `runtime.Callers` and render it lazily in `Error()`.
   - `Error()` output format: first line `"msg: cause"`, followed by rendered stack frames on subsequent lines.

2. `internal/errors/errors_test.go`
   - Verify `New` returns a plain stdlib error with no stack trace.
   - Verify `Wrap(nil, ...)` returns `nil`.
   - Verify `Wrap`/`Wrapf` start with `"msg: cause"` format.
   - Verify `Unwrap` works with stdlib `errors.Is`/`errors.As`.
   - Verify stack trace appears in `Error()` string for `Wrap`/`Wrapf` but not for `New`.

3. `internal/flock/flock.go`
   - Portable `Flock` struct holding `path string`, `*os.File`, and a `locked` flag.
   - `New(path string) *Flock` — stores the path but does not open the file (matches `gofrs/flock.New` behavior and keeps the signature error-free).
   - `TryLock() (bool, error)` — opens the file with `os.O_CREATE|os.O_RDWR` and mode `0644` if needed, then acquires an exclusive, non-blocking advisory lock.
     - Returns `(true, nil)` on success.
     - Returns `(false, nil)` if the file is already locked.
     - Returns `(false, err)` for actual system errors (open failure, etc.).
   - `Unlock() error` — release the lock. Safe to call when not locked.
   - `Close() error` — unlock if locked and close the underlying file.

4. `internal/flock/flock_unix.go`
   - Build tag: `//go:build unix`
   - Open the lock file with `os.O_CREATE|os.O_RDWR` and mode `0644` in `TryLock`.
   - `TryLock` uses `syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)`.
   - `Unlock` uses `syscall.Flock(fd, syscall.LOCK_UN)`.
   - Translate `syscall.EWOULDBLOCK` or `syscall.EAGAIN` into `(false, nil)` for `TryLock`.

5. `internal/flock/flock_windows.go`
   - Build tag: `//go:build windows`
   - Implementation using `syscall.LockFileEx` with an `OVERLAPPED` structure.
   - Open the lock file with `os.O_CREATE|os.O_RDWR` and mode `0644` so the handle has the access rights required by `LockFileEx` and the file is created if it does not exist.
   - `TryLock` calls `syscall.LockFileEx` with `LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY` (define the constants locally if `syscall` does not provide them) and a range covering the whole file (e.g., `0xFFFFFFFF, 0xFFFFFFFF`) so the call is non-blocking.
   - `Unlock` calls `syscall.UnlockFileEx` with the same `OVERLAPPED` and range used for locking.
   - Translate `ERROR_LOCK_VIOLATION` into `(false, nil)` for `TryLock`.

6. `internal/flock/doc.go`
   - Package documentation explaining:
     - Origin as a lightweight replacement for `github.com/gofrs/flock`.
     - Currently implemented: exclusive, non-blocking advisory file locking.
     - Not implemented: shared locks (`RLock`/`TryRLock`), blocking locks (`Lock`), timeouts, retry logic.

7. `internal/flock/flock_test.go`
   - Create a temp file, lock it, verify second `TryLock` returns `(false, nil)`.
   - Verify `Unlock`/`Close` release the lock (a second `Flock` can then acquire it).
   - Verify `Close` is safe to call on an already-closed or already-unlocked flock (behavior should be deterministic and documented).

## Files to Modify

1. `go.mod`
   - Update `go 1.13` to `go 1.26.0`.
   - Remove `require github.com/pkg/errors v0.9.1`.
   - Remove `github.com/gofrs/flock v0.7.1` from the second `require` block.

2. `go.sum`
   - Run `go mod tidy` after implementation to remove stale entries.

3. `queue.go`
   - Replace `import ("github.com/gofrs/flock"; "github.com/pkg/errors")` with internal packages.
   - The internal package is also named `flock`, so the field `fileLock *flock.Flock` can keep the same type name; only the import path changes.
   - Import the internal errors package with an alias (e.g., `dqueerrors`) to avoid shadowing stdlib `errors`.
   - Replace all `errors.New(...)` calls with `dqueerrors.New(...)`.
   - Replace all `errors.Wrap(...)` calls with `dqueerrors.Wrap(...)`.
   - Replace all `errors.Wrapf(...)` calls with `dqueerrors.Wrapf(...)`.

4. `segment.go`
   - Replace `import "github.com/pkg/errors"` with internal errors package (aliased, e.g., `dqueerrors`).
   - Replace all `errors.New(...)`, `errors.Wrap(...)`, and `errors.Wrapf(...)` calls.

5. `.github/workflows/test.yaml`
   - Update the Go version matrix from `[1.13, 1.11]` to `['1.26.0']` (quoted to prevent YAML from interpreting it as a number).
   - Update `actions/checkout@v1` to a recent version (e.g., `actions/checkout@v4`).
   - Update `actions/setup-go@v1` to a recent version (e.g., `actions/setup-go@v5`).
   - Keep the existing cross-compilation steps for Windows and Darwin.

## Implementation Steps

1. Update `go.mod`: set `go 1.26.0` and remove `github.com/pkg/errors` and `github.com/gofrs/flock` from `require` blocks.
2. Create `internal/errors/errors.go` and `internal/errors/errors_test.go`.
3. Run `go test ./internal/errors` to verify.
4. Create `internal/flock/flock.go`, `internal/flock/flock_unix.go`, `internal/flock/flock_windows.go`, `internal/flock/doc.go`, and `internal/flock/flock_test.go`.
5. Cross-compile the flock package:
   - `GOOS=linux go test -c ./internal/flock`
   - `GOOS=darwin go test -c ./internal/flock`
   - `GOOS=windows go test -c ./internal/flock`
6. Update `queue.go` and `segment.go` to use the internal packages.
7. Run `go mod tidy` to clean up `go.sum`.
   - If the local Go toolchain is newer than `1.26.0`, set `GOTOOLCHAIN=local` or manually ensure the `go.mod` `go` directive remains `1.26.0` and no unexpected `toolchain` directive is added.
8. Run `go test -race -cover ./...`.
9. Verify cross-compilation still works for `dque` on Windows and Darwin:
   - `GOOS=windows go test -c ./...`
   - `GOOS=darwin go test -c ./...`
10. Update `.github/workflows/test.yaml` to test Go `1.26.0` with modern action versions.

## Testing Plan

- Local: `go test -race -cover ./...`
- Cross-compile checks matching existing CI:
  - `GOOS=windows go test -c ./...`
  - `GOOS=darwin go test -c ./...`
- If possible, run unit tests on Windows and macOS to validate `internal/flock` behavior at runtime.

## Risks and Considerations

- **Stack traces**: `pkg/errors` includes stack traces in all its errors; the new helper captures them only in `Wrap`/`Wrapf`. Sentinels (`ErrQueueClosed`, `ErrEmpty`) remain plain stdlib errors, which matches how callers compare them with `==` today.
- **Error string compatibility**: The message/cause portion of `Wrap`/`Wrapf` errors remains `"msg: cause"`. The stack trace is appended afterward, so any code parsing only the first line is unaffected, but full-string comparisons will change.
- **Windows locking**: `syscall.LockFileEx` requires careful `OVERLAPPED` handling. The lock file must be opened with `os.O_RDWR` to ensure the handle has the access rights `LockFileEx` requires.
- **Platform support**: `internal/flock` supports Unix-like systems (`//go:build unix`) and Windows (`//go:build windows`). It does not support plan9, js/wasm, or other non-Unix/non-Windows platforms. This matches the practical platform coverage of the original `gofrs/flock` dependency.
- **API compatibility**: The public API of `dque` does not change. The `fileLock` field is unexported, so replacing its concrete type is safe.
- **Go 1.26.0**: Consumers on older Go versions will need to upgrade. This is intentional.
- **GitHub Actions**: The workflow must be updated to use action versions that support Go `1.26.0`; the current `v1` actions are too old. Quote the version string (`'1.26.0'`) in the YAML matrix to avoid parsing issues.
- **`go mod tidy` toolchain directive**: Running with a newer local toolchain could introduce a `toolchain` directive that raises the effective minimum above `1.26.0`. Review `go.mod` after tidying.
