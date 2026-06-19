# Thermo Review: dque PR #41 ("v2")

**Date:** 2026-06-15
**Scope:** Full diff of PR #41 merging `v2` branch (commits 173781c..7723fd1)
**Files changed:** 19 files, +1608/-450 lines

---

## 🔴 CRITICAL FINDINGS

### 1. `lock()` defer has a subtle correctness issue

**File:** `queue.go:473-476` | **Severity:** Low

```go
defer func() {
    if q.fileLock != fileLock {
        _ = fileLock.Close()
    }
}()
```

This works _only_ because constructors are not called concurrently. The comparison `q.fileLock != fileLock` relies on `q.fileLock` being nil at entry. If a future caller wrapped `New`/`Open` in a retry loop without proper synchronization, the defer could compare against a stale value. A cleaner pattern: use a local `success bool` flag.

### 2. Segment `close()` sets `file = nil` even on error

**File:** `segment.go:366-372` | **Severity:** Low-Medium

```go
if err := seg.file.Close(); err != nil {
    seg.file = nil                          // nil'd BEFORE returning error
    return dqueerrors.Wrapf(err, ...)
}
seg.file = nil
```

On a failed `Close()`, the file descriptor may still be open but `seg.file` is nil. Future retries of `close()` return nil (idempotent guard at line 366), but any code that checks `seg.file == nil` would incorrectly conclude the file is closed. Currently safe because `close()` is only called during shutdown/rollover where the segment reference is discarded, but the state tracking is incorrect.

**Fix:** Only nil `seg.file` on success:

```go
if err := seg.file.Close(); err != nil {
    return dqueerrors.Wrapf(err, ...)
}
seg.file = nil
return nil
```

### 3. `Close()` swallows errors after the first

**File:** `queue.go:200-221` | **Severity:** Medium

```go
var closeErr error
if err := q.firstSegment.close(); err != nil && closeErr == nil {
    closeErr = err
}
// ... more close operations, all guarded by `&& closeErr == nil`
```

If `firstSegment.close()` fails, but `lastSegment.close()` and `fileLock.Close()` _also_ fail, those errors are silently discarded. The queue is left in an unknown state but the caller only knows about the first problem. Cleanup always proceeds (segment refs nilled outside the guards), but error reporting is lossy.

---

## 🟡 HIGH SIGNAL FINDINGS

### 4. Dual closed-state indicators (`q.closed` vs `q.fileLock == nil`)

**File:** `queue.go:63, 196-220` | **Severity:** Medium (maintenance risk)

Two authoritative sources for "is the queue closed?":

- `q.closed` (boolean): checked by `Size()`, `SizeUnsafe()`, `SegmentNumbers()`, `Turbo()`
- `q.fileLock == nil`: checked by `Enqueue()`, `Dequeue()`, `Peek()`, `TurboOn/Off/Sync()`, `Close()`

Both are set in `Close()` only under the mutex. This will diverge if any future code path sets one but not the other. The `fileLock == nil` check is the "stronger" guard (prevents mutations), while `closed` is a "fast path" for read-only operations.

**Recommendation:** Pick one canonical indicator or document the dual-check pattern explicitly.

### 5. `SizeUnsafe()` now takes a lock — misleading name

**File:** `queue.go:438-448` | **Severity:** Medium (API contract change)

Previously unsynchronized. Now calls `q.mutex.Lock()` to check `q.closed` and snapshot segment pointers. Method name "Unsafe" implies "no synchronization, caller beware" but implementation is now partially synchronized. Callers who avoided `Size()` for performance (to skip locking) now pay lock overhead anyway.

**Recommendation:** Rename to `SizeFast()` or use atomics for `closed`.

### 6. `rand.Seed(0)` removed — test non-determinism

**File:** `queue_test.go:473` | **Severity:** Low-Medium

```diff
- rand.Seed(0) // ensure we have reproducible sleeps
```

`TestQueue_BlockingAggresive` now uses random sleeps without a fixed seed, making flakes harder to reproduce. No explanation in the commit message.

---

## 🟢 CODE QUALITY / MAINTAINABILITY

### 7. `validateQueueInputs` defense-in-depth is overengineered

**File:** `queue.go:71-83` | **Severity:** Low (style)

```go
if strings.ContainsAny(name, string(os.PathSeparator)+"/\\") { ... }
for _, part := range strings.Split(name, "/") { if part == ".." { ... } }
for _, part := range strings.Split(name, "\\") { if part == ".." { ... } }
```

The `strings.ContainsAny` check already catches `/`, `\`, and `os.PathSeparator`. The subsequent split-and-check loops for `..` are only needed if the name contains `..` without separators (which can't escape `dirPath` via `filepath.Join`). A simpler approach: `filepath.Clean(filepath.Join(dirPath, name))` then verify the prefix still matches `filepath.Clean(dirPath)`.

### 8. Constructor code duplication

**File:** `queue.go:99-175` | **Severity:** Low (style)

`New`, `Open`, and `NewOrOpen` share 10+ lines of identical struct initialization, lock, load, and error cleanup (~40 lines of duplication). A helper could consolidate this.

### 9. `internal/errors` stack capture renders on every `Error()` call

**File:** `internal/errors/errors.go:57-67` | **Severity:** Low (performance)

`captureStack()` skips exactly 3 frames — fragile if Go inlining changes. More importantly, `Error()` always renders the full stack trace string. On hot paths (`Enqueue`, `Dequeue`), every wrapped error that gets logged or printed will allocate and build a stack string. `pkg/errors` uses `%+v` formatting for this reason.

### 10. Windows `flock_windows.go` — verified correct

**File:** `internal/flock/flock_windows.go` | **Severity:** None

- Zero-initialized `Overlapped` struct: offset 0 + length `0xFFFFFFFF` = lock entire file ✅
- `hEvent` = NULL (zero), correct for synchronous locking ✅
- `errorLockViolation` = `0x21` matches `ERROR_LOCK_VIOLATION` ✅

### 11. `load()` error message inaccuracy

**File:** `queue.go:587` | **Severity:** Low

```go
if err := seg.delete(); err != nil {
    return dqueerrors.Wrap(err, "unable to delete empty segment in "+q.fullPath)
}
```

If `openQueueSegment` succeeds but `delete()` fails, the message is accurate. But if `openQueueSegment` fails inside the loop body (line 578), the error returned is about "unable to create queue segment", not about deletion. The surrounding loop logic correctly propagates whichever error occurs.

---

## ✅ POSITIVES

- **Path traversal hardening** is thorough and prevents directory escape from queue name
- **File descriptor leak** in `lock()` is properly fixed with the deferred close
- **Segment file handles** are now properly closed on `Close()` — this was a real resource leak in the old code
- **`t.TempDir()` migration** eliminates manual cleanup boilerplate and flaky leftover directories
- **Error sentinel usage** (`errors.Is(err, dque.ErrEmpty)`) is idiomatic and robust vs. string comparison
- **Internal flock** is clean, well-tested (299 lines of tests), and removes an external dependency
- **Empty segment cleanup on load** prevents segment-accumulation bloat over time
- **`itemsPerSegment` validation** catches zero/negative values that the old code silently accepted
- **`filepath` over `path`** is correct for OS-native path handling
- **`os.ReadDir` over `ioutil.ReadDir`** modernizes the API usage

---

## 🎯 RECOMMENDED ACTIONS (before merge)

1. **Fix segment `close()`** to only nil `file` on success (finding #2)
2. **Collect all errors in `Close()`** using `errors.Join` instead of first-error-only (finding #3)
3. **Restore `rand.Seed(0)`** or document why it was removed (finding #6)
4. **Consider** consolidating closed-state to a single indicator or documenting the dual-check pattern (finding #4)

---

## 📊 SEVERITY SUMMARY

| #   | Finding                                                 | Severity | File                           |
| --- | ------------------------------------------------------- | -------- | ------------------------------ |
| 1   | `lock()` defer pattern fragile under future concurrency | Low      | `queue.go:473`                 |
| 2   | Segment `close()` nils `file` before returning error    | Low-Med  | `segment.go:370`               |
| 3   | `Close()` swallows second-and-later errors              | Medium   | `queue.go:200`                 |
| 4   | Dual closed-state indicators risk divergence            | Medium   | `queue.go:63,196`              |
| 5   | `SizeUnsafe()` naming misleading after sync added       | Medium   | `queue.go:438`                 |
| 6   | `rand.Seed(0)` removed — test non-determinism           | Low-Med  | `queue_test.go:473`            |
| 7   | `validateQueueInputs` overengineered                    | Low      | `queue.go:71`                  |
| 8   | Constructor code duplication                            | Low      | `queue.go:99-175`              |
| 9   | Stack rendering in every `Error()` call                 | Low      | `internal/errors/errors.go:57` |
| 10  | Windows `LockFileEx` — verified correct                 | None     | `flock_windows.go`             |
| 11  | `load()` error message inaccuracy                       | Low      | `queue.go:587`                 |
