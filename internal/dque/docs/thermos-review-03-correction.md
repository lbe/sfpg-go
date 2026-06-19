# Thermos Review #3 — Correction Plan

**Date:** 2026-06-15
**Based on:** `thermos-review-03.md` (PR #41 "v2", 19 files, +1608/-450)
**Principle:** Interview-driven design (grill-me); all decisions resolved to shared understanding.

---

## Changes to Implement

### 1. Fix segment `close()` to only nil `file` on success (Finding #2)

**File:** `segment.go:366-372` | **Severity:** Low-Medium

Move `seg.file = nil` to after the error return, so `seg.file` truthfully reflects open/closed state:

```go
func (seg *qSegment) close() error {
    if seg.file == nil {
        return nil
    }
    if err := seg.file.Close(); err != nil {
        return dqueerrors.Wrapf(err, "unable to close segment file %s.", seg.fileName())
    }
    seg.file = nil
    return nil
}
```

---

### 2. Collect all errors in `Close()` using `errors.Join` (Finding #3)

**File:** `queue.go:200-221` | **Severity:** Medium

Replace `&& closeErr == nil` first-error-only guards with `errors.Join` to capture every cleanup failure:

```go
var closeErr error
if err := q.firstSegment.close(); err != nil {
    closeErr = errors.Join(closeErr, err)
}
if q.firstSegment != q.lastSegment {
    if err := q.lastSegment.close(); err != nil {
        closeErr = errors.Join(closeErr, err)
    }
}
if err := q.fileLock.Close(); err != nil {
    closeErr = errors.Join(closeErr, err)
}
```

`errors.Join` is available since Go 1.20; `go.mod` declares `go 1.26.0`.

The unconditional cleanup lines (`q.firstSegment = nil`, `q.lastSegment = nil`, `q.closed = true`, `q.fileLock = nil`, `q.emptyCond.Broadcast()`) remain unchanged — they always execute regardless of errors.

---

### 3. Restore deterministic seeding in `TestQueue_BlockingAggresive` (Finding #6)

**File:** `queue_test.go:473` | **Severity:** Low-Medium

Replace the global `rand.Intn(150)` with a local deterministic generator:

```go
rng := rand.New(rand.NewSource(0))
// ...
s := rng.Intn(150)
```

Update all `rand.Intn` calls in that test function to use `rng.Intn`.

---

### 4. Consolidate closed-state to single indicator `q.closed` (Finding #4)

**File:** `queue.go` | **Severity:** Medium

Replace all `q.fileLock == nil` closed-state checks with `q.closed`. `q.closed` becomes the single canonical "is closed?" indicator.

| Line | Method            | Old guard           | New guard  |
| ---- | ----------------- | ------------------- | ---------- |
| 195  | `Close()`         | `q.fileLock == nil` | `q.closed` |
| 235  | `Enqueue()`       | `q.fileLock == nil` | `q.closed` |
| 284  | `dequeueLocked()` | `q.fileLock == nil` | `q.closed` |
| 352  | `peekLocked()`    | `q.fileLock == nil` | `q.closed` |
| 487  | `TurboOn()`       | `q.fileLock == nil` | `q.closed` |
| 508  | `TurboOff()`      | `q.fileLock == nil` | `q.closed` |
| 532  | `TurboSync()`     | `q.fileLock == nil` | `q.closed` |

Methods already using `q.closed` (`Size()`, `SizeUnsafe()` → `SizeApprox()`, `SegmentNumbers()`, `Turbo()`) are unchanged.

The `q.fileLock = nil` assignment in `Close()` stays — it releases the underlying lock file resource. `q.closed = true` is set immediately before it, under the same mutex.

---

### 5. Use `acquired bool` flag in `lock()` defer (Finding #1)

**File:** `queue.go:622-641` | **Severity:** Low

Replace the `q.fileLock != fileLock` comparison with a local boolean flag:

```go
func (q *DQue) lock() error {
    l := filepath.Join(q.DirPath, q.Name, lockFile)
    fileLock := flock.New(l)

    acquired := false
    defer func() {
        if !acquired {
            _ = fileLock.Close()
        }
    }()

    locked, err := fileLock.TryLock()
    if err != nil {
        return err
    }
    if !locked {
        return dqueerrors.New("failed to acquire flock")
    }

    acquired = true
    q.fileLock = fileLock
    return nil
}
```

This is immune to future concurrent constructor scenarios.

---

### 6. Rename `SizeUnsafe()` → `SizeApprox()` (Finding #5)

**Files:** `queue.go`, `queue_test.go` | **Severity:** Medium

Rename the method and update its doc comment to reflect that it now takes a lock (for closed-state check) despite being an approximate snapshot:

- `queue.go:429` — doc comment and function declaration
- `queue.go:436` — function signature
- `queue_test.go:386` — caller `q.SizeUnsafe()` → `q.SizeApprox()`

The `CHANGELOG.md` reference to `SizeUnsafe` should also be updated.

---

### 7. Extract constructor helper `initQueue` (Finding #8)

**File:** `queue.go:99-175` | **Severity:** Low

Extract the shared struct initialization + lock + load + error cleanup from `New` and `Open` into a private method:

```go
func (q *DQue) initQueue(fullPath string, itemsPerSegment int, builder func() interface{}) error {
    q.fullPath = fullPath
    q.config.ItemsPerSegment = itemsPerSegment
    q.builder = builder
    q.emptyCond = sync.NewCond(&q.mutex)
    if err := q.lock(); err != nil {
        return err
    }
    if err := q.load(); err != nil {
        if releaseErr := q.fileLock.Close(); releaseErr != nil {
            return errors.Join(err, releaseErr)
        }
        return err
    }
    return nil
}
```

`New` and `Open` become: validate inputs → create/open directory → `q := DQue{Name, DirPath}` → `q.initQueue(...)`. `NewOrOpen` benefits automatically since it delegates to `New`/`Open`.

---

## Deferred / No Action

| #   | Finding                                 | Disposition               | Rationale                                                                             |
| --- | --------------------------------------- | ------------------------- | ------------------------------------------------------------------------------------- |
| 7   | `validateQueueInputs` overengineered    | **Keep as-is**            | Correct today; simplification carries risk. Defer to follow-up with dedicated review. |
| 9   | Stack rendering in every `Error()` call | **Design choice — leave** | Intentional tradeoff. Defer to design discussion.                                     |
| 10  | Windows `LockFileEx` verified correct   | **No action**             | Confirmed correct.                                                                    |
| 11  | `load()` error message inaccuracy       | **No action**             | Both error paths are accurate as-is.                                                  |

---

## Implementation Order

1. **#5 — `acquired` flag in `lock()`** (standalone, low risk)
2. **#2 — Segment `close()` nil-on-error fix** (standalone, low risk)
3. **#8 — Extract `initQueue` helper** (mechanical refactor, enables clean diff for #4)
4. **#4 — Consolidate closed-state to `q.closed`** (touches 7 sites; do after #8 to avoid conflicts)
5. **#6 — Rename `SizeUnsafe()` → `SizeApprox()`** (do after #4 since it touches the same closed-check logic)
6. **#3 — `errors.Join` in `Close()`** (standalone, but do after #4 for clean diff on the Close guard)
7. **#6 (test) — `rand.New(rand.NewSource(0))`** (standalone)
