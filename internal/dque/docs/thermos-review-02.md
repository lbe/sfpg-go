# Thermos Review Synthesis — `v2` branch

**Scope:** Remove `github.com/pkg/errors` and `github.com/gofrs/flock` by adding `internal/errors` + `internal/flock`, bump Go to `1.26.0`, modernize CI/tests.

**Verification run by reviewers:** `go test -race -cover ./...` passes (~88.5% overall, 100% for the two new internal packages), `go vet ./...` passes, Windows/Darwin cross-compile passes. The synthesis author also reproduced the passing test run and cross-compile locally.

---

## Blockers / highest priority

| #   | Finding                                                         | Files                                               | Evidence / note                                                                                                                                                                                                                                                                         |
| --- | --------------------------------------------------------------- | --------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | **File-descriptor leak when `TryLock` fails**                   | `queue.go:577–590`, `internal/flock/flock.go:26–47` | `lock()` creates a `Flock`, calls `TryLock()`, and on `(false, nil)` or `(false, err)` returns without `Close()`. `TryLock()` deliberately leaves the file open on failure to match `gofrs/flock`, but the caller is not cleaning up. This is a real leak on every failed open attempt. |
| 2   | **Code is not `gofmt`-clean**                                   | `queue.go`, `benchmark_test.go`                     | `gofmt -l .` reports both files. Trivial but should not land.                                                                                                                                                                                                                           |
| 3   | **Lint CI workflow is incompatible with Go 1.26**               | `.github/workflows/lint.yaml`                       | Still uses `actions/checkout@v1` and `golangci-lint v1.22.2` (2020); that version will not parse Go 1.26 source. `test.yaml` was updated, but `lint.yaml` was missed.                                                                                                                   |
| 4   | **`internal/flock` does not build on `plan9`, `js/wasm`, etc.** | `internal/flock/flock_unix.go`, `flock_windows.go`  | Confirmed: `GOOS=plan9 go test -c ./internal/flock` and `GOOS=js GOARCH=wasm …` fail with undefined `tryLock`/`unlock`. Either add a `!unix && !windows` stub or document that these platforms are unsupported and fail intentionally.                                                  |

## Medium priority

| #   | Finding                                             | Files                                                        | Evidence / note                                                                                                                                                                                                        |
| --- | --------------------------------------------------- | ------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 5   | **Stack traces rendered inside `Error()`**          | `internal/errors/errors.go:52–59`                            | Flagged by both reviewers. This is **by design** in the refactor plan, but it is a behavioral change: any caller doing `err.Error() == "..."` on a wrapped error will break. Document prominently.                     |
| 6   | **`Flock.Close` leaks file handle if unlock fails** | `internal/flock/flock.go:62–79`                              | Returns early on unlock error and never reaches `f.file.Close()`. Should close the file and join errors.                                                                                                               |
| 7   | **Data races on closed-state checks**               | `queue.go` (`Size`, `SizeUnsafe`, `SegmentNumbers`, `Turbo`) | These methods read `q.fileLock` / `q.turbo` outside the mutex while `Close()` writes `q.fileLock = nil` under the lock. Likely **pre-existing**, but the branch touches `Close` and should at least not make it worse. |
| 8   | **`path.Join` used for filesystem paths**           | `queue.go`, `segment.go`                                     | Pre-existing portability wart. Worth fixing while the file-path code is being touched, but not new.                                                                                                                    |
| 9   | **Path traversal via queue `name`**                 | `queue.go:70–86`                                             | `name` is only checked for emptiness; `../` segments allow creation outside `dirPath`. Pre-existing, but a genuine security issue.                                                                                     |
| 10  | **Tests are not hermetic**                          | `queue_test.go`, `segment_test.go`                           | Many tests use hardcoded names like `"test1"`, `"./TestSegment"` and manual `os.RemoveAll`. The new tests mostly use `t.TempDir()`, but legacy tests still pollute the working directory and can cross-contaminate.    |
| 11  | **`itemsPerSegment` not validated**                 | `queue.go` constructors                                      | Zero/negative values cause unbounded segment creation. Pre-existing.                                                                                                                                                   |
| 12  | **`load()` edge case: all segments empty+complete** | `queue.go:531–547`                                           | If every on-disk segment is empty and complete, `minNum` increments past `maxNum` and `openQueueSegment` fails. Reachable only via corruption/manual manipulation, but should fall back to creating segment 1.         |

## Low priority / polish

- `TestQueue_UseAfterClose` compares `err.Error()` strings instead of using `errors.Is` / sentinel identity.
- Duplicated `assert` helper across `queue_test.go` and `segment_test.go`.
- Duplicated constructor validation in `New`/`Open`/`NewOrOpen`.
- Typos: `segment.go:316` has `ssegments`; a few grammar nits remain.
- Public API changes in v2 (`Config` → `config`, `DQUE_EMPTY` → `ErrEmpty`, etc.) are not documented in a `CHANGELOG.md`.
- Windows `LockFileEx` implementation is only cross-compiled, never runtime-tested in CI.
- `segment.close()` does not nil out `seg.file` after closing.

## Uncertainty / could not fully confirm

- **Flaky `TestQueue_BlockingAggresive`**: the code-quality reviewer observed intermittent `-race` failures (timeout + “file already exists”), but the synthesis author ran it `-race -count=3` locally and it passed every time. Treat as a possible flake; if it fails in CI, investigate the goroutine choreography and switch it to `t.TempDir()`.
- **`TestQueue_Add9Remove8` stale segment numbers**: reviewer claims the post-reopen assertions use pre-close variables because the reopened queue is assigned to `_`. The code does look that way on inspection; worth fixing.

## Unified verdict

The dependency removal itself is sound: the two new internal packages are small, well-tested, and drop the external dependencies successfully. The branch is close to mergeable, but **fix the FD leak in `queue.lock()`, run `gofmt`, and update `lint.yaml` first**. The non-Unix/Windows build failure is also a blocker unless the project explicitly decides to drop those targets. Most other findings are either pre-existing warts or design choices (stack traces in `Error()`) that should simply be documented.
