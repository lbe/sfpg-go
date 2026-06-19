# Thermos Review #4 — dque Full Codebase Audit

**Date:** 2026-06-15
**Scope:** Full repository review (all source files, not just diff)
**Base commit:** 7723fd1 (merge PR #41)
**Head commit:** c4cc6c1 (temp wip)

---

## Critical Issues

### 1. `qSegment.remove()` returns `nil` on `_sync()` failure — silent data loss

- **Severity:** HIGH
- **File:** `segment.go` ~line 201
- When `fsync` fails in non-turbo mode, `remove()` returns `return nil, err` instead of `return object, err`. By this point the object has already been removed from `seg.objects` and a deletion marker written to disk — the item is permanently lost. The caller receives `(nil, error)` and the item evaporates.
- **Fix:** Change `return nil, err` to `return object, err`.

### 2. `qSegment.add()` appends to in-memory slice before `_sync()` — inconsistency on error

- **Severity:** HIGH
- **File:** `segment.go` ~lines 243-246
- If `_sync()` fails, `add()` returns an error but the object is already in `seg.objects`. The caller thinks the enqueue failed, but the item is in the queue. Retries cause duplicates; discarding causes phantom items.
- **Fix:** Move `append` after `_sync()`, or roll back `seg.objects` on error.

### 3. `SizeApprox()` has an ABA data race — can produce nonsensical results or crash

- **Severity:** HIGH
- **File:** `queue.go` ~lines 404-420
- `SizeApproq()` drops `q.mutex`, then calls methods on copied segment pointers. Between unlock and use, `Dequeue()` can replace and `delete()` those segments, closing their file handles. The "approximate" framing papers over a real race.
- **Fix:** Hold `q.mutex` and read values directly, or copy all needed fields under a single lock acquisition.

---

## Major Issues

### 4. Redundant `seg.mutex` adds overhead with zero concurrency benefit

- **Severity:** MAJOR (structural)
- **File:** `segment.go` (every method)
- Every `qSegment` method locks `seg.mutex`, but all callers already hold `q.mutex`. The segment mutex provides nothing — pure overhead and cognitive load across ~20 lock/unlock pairs.
- **Fix:** Delete `seg.mutex`, remove all `Lock()`/`Unlock()` calls on it, access segment fields directly under `q.mutex`.

### 5. `internal/errors/` package adds complexity and breaks ergonomics for no production value

- **Severity:** MAJOR (structural)
- **File:** `internal/errors/errors.go` (98 lines + 107 lines of tests)
- Custom error wrapping with stack traces is fragile: tests hardcode `err.Error()` string prefixes that include rendered stack frames (break on compiler/inline changes). Sentinel errors use plain `errors.New` internally (no stack trace) while wrapped errors get one — inconsistent behavior. Standard library's `fmt.Errorf("%w", err)` already supports everything this package does.
- **Fix:** Delete the package; replace `Wrap`/`Wrapf` with `fmt.Errorf` using `%w`.

### 6. File descriptor leak in `queue.load()` error path

- **Severity:** MAJOR
- **File:** `queue.go` ~lines 557-580
- If `load()` opens the first usable segment successfully, then fails to open the last segment, the first segment's file handle is never closed. `initQueue()` only releases `q.fileLock`.
- **Fix:** Close `q.firstSegment` on the error path in `initQueue()`.

### 7. Test infrastructure: triplicated helpers, identical types, custom assert

- **Severity:** MAJOR (structural)
- **Files:** `queue_test.go` (841 lines), `segment_test.go` (388 lines), `internal/testutil/assert.go` (18 lines)
- Three nearly identical helpers (`newQ`, `openQ`, `newOrOpenQ`). `item1`/`item2`/`item3` are identical structs with different names across test files. `testutil.Assert` uses ANSI color codes, `FailNow()` semantics, and adds nothing over `t.Fatalf`.
- **Fix:** Collapse helpers into one parameterized function, unify test types, delete `testutil.Assert` in favor of `t.Fatalf`.

---

## Moderate Issues

### 8. `New`/`Open` constructors are ~90% identical

- **File:** `queue.go`
- Only differ in `dirExists` check + `Mkdir`. Extract a common core or use a `create` flag parameter.

### 9. `validateQueueInputs` has redundant dead-code loops

- **File:** `queue.go`
- The two `for` loops splitting on `/` and `\\` to check for `..` segments are unreachable after the prior `strings.ContainsAny` + exact-match checks.

### 10. `TurboOn`/`TurboOff` have asymmetric redundant guards at two levels

- **File:** `queue.go`, `segment.go`
- Queue-level methods guard against double-calls; segment-level methods also guard (but queue-level always catches first). Consolidate to one level.

### 11. `Dequeue()` returns `(obj, error)` where error means "inconsistent state"

- **File:** `queue.go` (`dequeueLocked`)
- Three call sites return the dequeued object alongside an error indicating corruption. Callers can't distinguish "here's your item, but something else broke" from "total failure."

### 12. `delete()` error path can leak file descriptor

- **File:** `segment.go`
- If `seg.file.Close()` fails in `delete()`, the function returns early without setting `seg.file = nil`.

### 13. `dirExists`/`fileExists` silently swallow OS errors

- **File:** `util.go`
- Permission denied and other errors from `os.Stat` are conflated with "doesn't exist." Inline at call sites or propagate errors.

---

## Minor Issues

### 14. Unbounded allocation from untrusted `gobLen` in `load()`

- **File:** `segment.go`
- A corrupted segment file can trigger a 4GB allocation. Low severity (local trusted data store) but worth a sanity cap.

### 15. Lock file (`lock.lock`) never cleaned up

- **File:** `queue.go`
- Standard practice for flock, but worth noting.

### 16. Comment references nonexistent `mutexEmptyCond`

- **File:** `queue.go` struct docs
- Should reference `q.mutex`.

### 17. `Close()` nils segments before broadcasting

- **File:** `queue.go`
- Ordering is `firstSegment = nil` → `lastSegment = nil` → `closed = true` → `Broadcast()`. Should set `closed = true` before nil-ing segments.

---

## Summary

| #   | Severity | File                    | Issue                                                                |
| --- | -------- | ----------------------- | -------------------------------------------------------------------- |
| 1   | HIGH     | `segment.go`            | `remove()` returns `nil` on `_sync()` failure — data loss            |
| 2   | HIGH     | `segment.go`            | `add()` modifies in-memory state before `_sync()` — inconsistency    |
| 3   | HIGH     | `queue.go`              | `SizeApprox()` ABA data race                                         |
| 4   | MAJOR    | `segment.go`            | Redundant `seg.mutex` — pure overhead                                |
| 5   | MAJOR    | `internal/errors/`      | Custom error package adds complexity, breaks string comparison       |
| 6   | MAJOR    | `queue.go`              | File descriptor leak in `load()` error path                          |
| 7   | MAJOR    | test files              | Triplicated helpers, identical types, custom assert                  |
| 8   | MODERATE | `queue.go`              | `New`/`Open` constructors ~90% identical                             |
| 9   | MODERATE | `queue.go`              | `validateQueueInputs` redundant dead-code loops                      |
| 10  | MODERATE | `queue.go`/`segment.go` | `TurboOn`/`TurboOff` redundant two-level guards                      |
| 11  | MODERATE | `queue.go`              | `Dequeue()` returns `(obj, error)` with inconsistent-state semantics |
| 12  | MODERATE | `segment.go`            | `delete()` error path can leak file descriptor                       |
| 13  | MODERATE | `util.go`               | `dirExists`/`fileExists` swallow OS errors                           |
| 14  | LOW      | `segment.go`            | Unbounded allocation from untrusted `gobLen`                         |
| 15  | LOW      | `queue.go`              | Lock file never cleaned up                                           |
| 16  | LOW      | `queue.go`              | Comment references nonexistent `mutexEmptyCond`                      |
| 17  | LOW      | `queue.go`              | `Close()` nils segments before broadcasting                          |

---

## Overall Assessment

The codebase is fundamentally sound for a small durability library — the segment-based FIFO design is clean and the test suite is thorough. Issues are concentrated in:

1. **Error-handling wrapper** (`internal/errors/`) adding complexity without proportional value
2. **Dual-mutex pattern** (`q.mutex` + `seg.mutex`) that looks careful but is actually unnecessary
3. **Edge cases in `add()`/`remove()` error paths** that can cause silent data inconsistency
4. **`SizeApprox()` data race** that is documented as "approximate" but is actually unsound

The highest-priority fixes are the `remove()` data-loss bug (#1), the `add()` inconsistency (#2), and the `SizeApprox()` race (#3). The highest-impact structural improvements are removing `seg.mutex` (#4) and deleting the custom errors package (#5).
