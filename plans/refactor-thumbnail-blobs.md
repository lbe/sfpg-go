# Plan: Refactor thumbnail_blobs to thumbs.db

## Overview

`thumbnail_blobs` has been manually moved from `sfpg.db` to `thumbs.db` (copied, dropped, vacuumed).
This plan wires up the Go code so the application ATTACHes `thumbs.db` on every pooled connection
and routes all thumbnail blob queries through `thumbs.thumbnail_blobs`.

## Agent Operating Rules

- **All approved plan actions may proceed without user approval.**
- An agent **MUST** stop immediately if any deviation from the plan is thought to be needed.
  Present the issue to the user and await guidance before continuing.
- Each numbered step below runs in a **separate sub-agent invocation**.
- Every change step is immediately followed by a dedicated validation step (also a separate sub-agent).
- If a validation step finds a problem it **may correct it in place**, but must then trigger an
  **additional validation sub-agent** to confirm the correction.
- **TDD is mandatory.** For every functional code change, failing tests are written first, then
  the implementation is written to make them pass (Red → Green → Refactor).
- **The build must never be broken between commits.** `go build -o /dev/null ./...` must pass
  after every committed step, no exceptions.
- Model selection guidance:
  - SQL-only or mechanical single-file edits → **Gemini Flash 3.0**
  - Multi-file orchestration, complex logic, TDD cycles → **Claude Sonnet 4.6**
  - Final integration validation → **Claude Sonnet 4.6**

## Key Technical Facts (read before executing any step)

- `thumbnail_exists_view` does **NOT** join `thumbnail_blobs` — it joins `thumbnails` only. No view update needed.
- The `QueriesFunc` field on `dbconnpool.Config` is currently **never called** — `newCpConn` hardcodes
  `gallerydb.PrepareCustomQueries`. The ATTACH must be inserted directly into `newCpConn`.
- ATTACH alias is `thumbs` (not `blob_db`).
- SQLite does not enforce cross-DB foreign keys. The thumbs.db schema must declare `thumbnail_id`
  **without** the `REFERENCES thumbnails(id) ON DELETE CASCADE` constraint.
- Three SQL constants in `custom.sql.go` reference bare `thumbnail_blobs` and must use `thumbs.thumbnail_blobs`.
- `setupTestDB` in `gallerydb_integration_test.go` uses an in-memory `:memory:` database. SQLite
  in-memory databases **can** ATTACH file-based databases. If the driver disallows it, switch
  `setupTestDB` to use a temp file DB (same function signature).
- All tests that use pools need `ThumbsDBPath` set in pool Config.
- `migrations/migrations.go` embeds `migrations/*.sql`. A new `ThumbsFS` embed and
  `NewThumbsMigrator` function are needed for `migrations/thumbs/*.sql`.
- `database.Setup()` currently returns `(string, *Pool, *Pool, error)`. This must change to
  `(DatabasePaths, *Pool, *Pool, error)`. `server/app.go` uses `database.Setup()` in four places
  and uses `database.RecreatePoolsWithConfig()` in one place — all must be updated in the **same
  commit** as the `database/app.go` change to keep the build green.

## File Change Map

| File                                                         | Change                                                                                                                 |
| ------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------- |
| `migrations/migrations/012_move_blobs_to_thumbs_db.up.sql`   | `DROP TABLE IF EXISTS thumbnail_blobs`                                                                                 |
| `migrations/migrations/012_move_blobs_to_thumbs_db.down.sql` | Recreate `thumbnail_blobs` in sfpg.db                                                                                  |
| `migrations/thumbs/012_init_thumbs.up.sql`                   | `CREATE TABLE IF NOT EXISTS thumbnail_blobs` (no FK)                                                                   |
| `migrations/thumbs/012_init_thumbs.down.sql`                 | `DROP TABLE IF EXISTS thumbnail_blobs`                                                                                 |
| `migrations/migrations.go`                                   | Add `ThumbsFS` embed + `NewThumbsMigrator()`                                                                           |
| `migrations/migrations_test.go`                              | Add `TestThumbsMigration` (written **before** Step 3 implementation)                                                   |
| `internal/dbconnpool/dbconnpool.go`                          | Add `ThumbsDBPath` to Config; ATTACH in `newCpConn`                                                                    |
| `internal/dbconnpool/dbconnpool_test.go`                     | Add test verifying ATTACH occurs (written **before** Step 5 implementation)                                            |
| `internal/server/database/app.go`                            | Add `DatabasePaths` struct; `migrateBlobsDB()`; update `Setup()`, `createDatabasePools()`, `RecreatePoolsWithConfig()` |
| `internal/gallerydb/custom.sql.go`                           | Qualify three SQL constants with `thumbs.`                                                                             |
| `internal/server/app.go`                                     | `dbPath string` → `dbPaths database.DatabasePaths`; update all callers                                                 |
| `internal/gallerydb/gallerydb_integration_test.go`           | `setupTestDB` needs thumbs ATTACH                                                                                      |
| `internal/server/files/service_test.go`                      | `createTestPoolsAndDir` needs `ThumbsDBPath` in pool Config                                                            |

---

## ✅ Step 1 — sfpg.db Migration 012 SQL Files (Gemini Flash 3.0) — COMPLETE

Create `migrations/migrations/012_move_blobs_to_thumbs_db.up.sql`:

```sql
-- Migration 012: Remove thumbnail_blobs from main DB (moved to thumbs.db)
DROP TABLE IF EXISTS thumbnail_blobs;
```

Create `migrations/migrations/012_move_blobs_to_thumbs_db.down.sql`:

```sql
-- Migration 012 down: Restore thumbnail_blobs to main DB
CREATE TABLE IF NOT EXISTS thumbnail_blobs (
  thumbnail_id INTEGER PRIMARY KEY REFERENCES thumbnails(id) ON DELETE CASCADE,
  data         BLOB NOT NULL
);
```

Then run:

```bash
make format
go build -o /dev/null ./...
```

Commit message: `migrations: add 012 to drop thumbnail_blobs from sfpg.db`

---

## ✅ Step 2 — Validate Step 1 (Gemini Flash 3.0) — COMPLETE

- Confirm both files exist with correct content.
- Confirm `go build -o /dev/null ./...` succeeds.
- Confirm file names match `NNN_name.{up,down}.sql` pattern.
- Run: `mkdir -p tmp && go test -tags integration ./migrations/... > ./tmp/test_output.txt 2>&1`
- `grep -E "FAIL|ERROR" ./tmp/test_output.txt` must be empty.

---

## ✅ Step 3 — thumbs.db Migration: Write Failing Test First, Then Implement (Claude Sonnet 4.6) — COMPLETE

### 3a — Write the failing test (RED)

In `migrations/migrations_test.go`, add:

```go
func TestThumbsMigration(t *testing.T) {
    dbfile := filepath.Join(t.TempDir(), "test_thumbs.db")
    migrator, err := NewThumbsMigrator(dbfile)
    if err != nil {
        t.Fatalf("NewThumbsMigrator: %v", err)
    }
    defer migrator.Close()

    if err := migrator.Up(); err != nil && err != migrate.ErrNoChange {
        t.Fatalf("thumbs Up: %v", err)
    }

    db, err := sql.Open("sqlite3", dbfile)
    if err != nil {
        t.Fatalf("open thumbs db: %v", err)
    }
    defer db.Close()

    var name string
    err = db.QueryRowContext(context.Background(),
        `SELECT name FROM sqlite_master WHERE type='table' AND name='thumbnail_blobs'`).Scan(&name)
    if err != nil {
        t.Fatalf("thumbnail_blobs table not found: %v", err)
    }
    if name != "thumbnail_blobs" {
        t.Errorf("expected thumbnail_blobs, got %s", name)
    }
}
```

Confirm test **fails** (compile error or test failure — `NewThumbsMigrator` does not exist yet):

```bash
mkdir -p tmp && go test -tags integration ./migrations/... > ./tmp/test_output.txt 2>&1
grep -E "FAIL|ERROR" ./tmp/test_output.txt
```

### 3b — Implement (GREEN)

Create directory `migrations/thumbs/`.

Create `migrations/thumbs/012_init_thumbs.up.sql`:

```sql
-- Migration 012: Initialize thumbnail_blobs in thumbs.db
-- No foreign key constraint: SQLite does not enforce cross-database FK references.
CREATE TABLE IF NOT EXISTS thumbnail_blobs (
  thumbnail_id INTEGER PRIMARY KEY,
  data         BLOB NOT NULL
);
```

Create `migrations/thumbs/012_init_thumbs.down.sql`:

```sql
DROP TABLE IF EXISTS thumbnail_blobs;
```

Update `migrations/migrations.go` — add below the existing `FS` declaration:

```go
//go:embed thumbs/*.sql
var ThumbsFS embed.FS

// NewThumbsMigrator creates a migrator for the thumbnail blob database (thumbs.db).
func NewThumbsMigrator(dbPath string) (*migrate.Migrate, error) {
	d, err := iofs.New(ThumbsFS, "thumbs")
	if err != nil {
		return nil, fmt.Errorf("create thumbs migrations source: %w", err)
	}

	var dsn string
	if dbPath == ":memory:" {
		dsn = "sqlite://:memory:"
	} else {
		dsn = "sqlite://" + filepath.ToSlash(dbPath)
	}

	m, err := migrate.NewWithSourceInstance("iofs", d, dsn)
	if err != nil {
		return nil, fmt.Errorf("initialize thumbs migrator: %w", err)
	}
	return m, nil
}
```

Then run:

```bash
make format
go build -o /dev/null ./...
mkdir -p tmp && go test -tags integration ./migrations/... > ./tmp/test_output.txt 2>&1
grep -E "FAIL|ERROR" ./tmp/test_output.txt
```

Both must pass (empty grep output = no failures).

Commit message: `migrations: add thumbs.db 012 migration and NewThumbsMigrator`

---

## ✅ Step 4 — Validate Step 3 (Gemini Flash 3.0) — COMPLETE

- Confirm `migrations/thumbs/` exists with both SQL files.
- Confirm `ThumbsFS` and `NewThumbsMigrator` are in `migrations/migrations.go`.
- Confirm `go build -o /dev/null ./...` succeeds.
- Run: `mkdir -p tmp && go test -tags integration ./migrations/... > ./tmp/test_output.txt 2>&1`
- `grep -E "FAIL|ERROR" ./tmp/test_output.txt` must be empty.
- Confirm `TestThumbsMigration` appears in output as PASS.

---

## ✅ Step 5 — dbconnpool: Write Failing Test, Then Add ThumbsDBPath + ATTACH (Claude Sonnet 4.6) — COMPLETE

### 5a — Write the failing test (RED)

Locate `internal/dbconnpool/` and check for an existing `_test.go` file. If none, create
`internal/dbconnpool/dbconnpool_test.go`. Add a test that creates a pool with `ThumbsDBPath`
set and verifies the `thumbs` attachment exists on a checked-out connection:

```go
//go:build integration

package dbconnpool_test

import (
    "context"
    "path/filepath"
    "testing"

    _ "github.com/ncruces/go-sqlite3/driver"
    _ "github.com/ncruces/go-sqlite3/embed"

    "github.com/lbe/sfpg-go/internal/dbconnpool"
    "github.com/lbe/sfpg-go/migrations"
)

func TestThumbsDBAttach(t *testing.T) {
    tempDir := t.TempDir()
    mainDBPath := filepath.Join(tempDir, "test.db")
    thumbsDBPath := filepath.Join(tempDir, "thumbs.db")

    // Initialize thumbs.db via migration
    m, err := migrations.NewThumbsMigrator(thumbsDBPath)
    if err != nil {
        t.Fatalf("NewThumbsMigrator: %v", err)
    }
    if err := m.Up(); err != nil {
        m.Close()
        t.Fatalf("thumbs migrate up: %v", err)
    }
    m.Close()

    ctx := context.Background()
    pool, err := dbconnpool.NewDbSQLConnPool(ctx,
        "file:"+filepath.ToSlash(mainDBPath)+"?mode=rwc&_pragma=journal_mode(WAL)",
        dbconnpool.Config{
            DriverName:         "sqlite3",
            MaxConnections:     1,
            MinIdleConnections: 1,
            ThumbsDBPath:       thumbsDBPath,
        })
    if err != nil {
        t.Fatalf("NewDbSQLConnPool: %v", err)
    }
    defer pool.Close()

    cpc, err := pool.Get()
    if err != nil {
        t.Fatalf("pool.Get: %v", err)
    }
    defer pool.Put(cpc)

    // Verify thumbs schema is visible
    var name string
    err = cpc.Conn.QueryRowContext(ctx,
        `SELECT name FROM thumbs.sqlite_master WHERE type='table' AND name='thumbnail_blobs'`,
    ).Scan(&name)
    if err != nil {
        t.Fatalf("thumbs.thumbnail_blobs not visible after ATTACH: %v", err)
    }
    if name != "thumbnail_blobs" {
        t.Errorf("expected thumbnail_blobs, got %q", name)
    }
}
```

Confirm test **fails** (`ThumbsDBPath` field does not exist yet):

```bash
mkdir -p tmp && go test -tags integration ./internal/dbconnpool/... > ./tmp/test_output.txt 2>&1
grep -E "FAIL|ERROR" ./tmp/test_output.txt
```

### 5b — Implement (GREEN)

In `internal/dbconnpool/dbconnpool.go`:

**Add to `Config` struct** (after `QueriesFunc` field):

```go
// ThumbsDBPath is the path to the thumbnail blob database (thumbs.db).
// When non-empty, each new connection will ATTACH this database as "thumbs"
// before preparing statements, enabling cross-database queries.
ThumbsDBPath string
```

**Update `newCpConn`** — insert the ATTACH block between the ping and the `PrepareCustomQueries` call:

```go
// ATTACH the thumbs database on this connection if configured.
// The attachment is connection-scoped: each *sql.Conn requires its own ATTACH.
if p.Config.ThumbsDBPath != "" {
    attachSQL := fmt.Sprintf("ATTACH DATABASE 'file:%s' AS thumbs", filepath.ToSlash(p.Config.ThumbsDBPath))
    if _, err = conn.ExecContext(p.ctx, attachSQL); err != nil {
        conn.Close()
        return nil, fmt.Errorf("attach thumbs database: %w", err)
    }
}
```

Add `"path/filepath"` to imports if not already present.

Then run:

```bash
make format
make lint
go build -o /dev/null ./...
mkdir -p tmp && go test -tags integration ./internal/dbconnpool/... > ./tmp/test_output.txt 2>&1
grep -E "FAIL|ERROR" ./tmp/test_output.txt
```

All must pass.

Commit message: `dbconnpool: add ThumbsDBPath config field and per-connection ATTACH`

---

## ✅ Step 6 — Validate Step 5 (Gemini Flash 3.0) — COMPLETE

- Confirm `ThumbsDBPath` field exists in Config with correct doc comment.
- Confirm ATTACH block is in `newCpConn` between ping and `PrepareCustomQueries`.
- Confirm ATTACH is skipped when `ThumbsDBPath == ""`.
- Confirm `go build -o /dev/null ./...` succeeds.
- Confirm `make lint` passes.
- Run: `mkdir -p tmp && go test -tags integration ./internal/dbconnpool/... > ./tmp/test_output.txt 2>&1`
- `grep -E "FAIL|ERROR" ./tmp/test_output.txt` must be empty.
- Confirm `TestThumbsDBAttach` appears as PASS.

---

## ✅ Step 7 — database/app.go + server/app.go: DatabasePaths (Claude Sonnet 4.6) — COMPLETE

**These two files must change in the same commit** to keep `go build -o /dev/null ./...` green.

### Changes to `internal/server/database/app.go`

**Add `DatabasePaths` struct** (near top, before `Setup`):

```go
// DatabasePaths holds the file paths for both application databases.
type DatabasePaths struct {
    Main   string // Path to sfpg.db
    Thumbs string // Path to thumbs.db
}
```

**Add `migrateBlobsDB` function**:

```go
func migrateBlobsDB(dbPath string) error {
    db, err := os.OpenFile(dbPath, os.O_RDWR|os.O_CREATE, 0o666)
    if err != nil {
        return fmt.Errorf("failed to open thumbs database file: %w", err)
    }
    db.Close()

    m, err := migrations.NewThumbsMigrator(dbPath)
    if err != nil {
        return fmt.Errorf("failed to create thumbs migrator: %w", err)
    }
    defer m.Close()

    if err := m.Up(); err != nil && err != migrate.ErrNoChange {
        return fmt.Errorf("thumbs up migration failed: %w", err)
    }
    return nil
}
```

**Update `Setup()`**:

- Return type: `(string, *dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error)` → `(DatabasePaths, *dbconnpool.DbSQLConnPool, *dbconnpool.DbSQLConnPool, error)`
- Add `thumbsDBPath := filepath.Join(dbDir, "thumbs.db")` after `dbPath` is set.
- Call `migrateBlobsDB(thumbsDBPath)` after `migrateDB(dbPath)`.
- Pass `thumbsDBPath` into `createDatabasePools`.
- Return `DatabasePaths{Main: dbPath, Thumbs: thumbsDBPath}` instead of bare `dbPath`.
- All `return "", nil, nil, err` → `return DatabasePaths{}, nil, nil, err`.

**Update `createDatabasePools()`**:

- Add `thumbsDBPath string` parameter.
- Add `ThumbsDBPath: thumbsDBPath` to both RW and RO pool `Config` structs.

**Remove dead placeholder in `PrepareCustomQueries`** in `internal/gallerydb/custom.sql.go`:

```go
// Remove this stale no-op — ATTACH now lives in newCpConn:
_ = db // Placeholder for future ATTACH logic if needed in PrepareContext
```

**Update `RecreatePoolsWithConfig()`**:

- Change `dbPath string` parameter → `paths DatabasePaths`.
- Use `paths.Main` for DSN construction.
- Pass `paths.Thumbs` as `thumbsDBPath` to `createDatabasePools`.

### Changes to `internal/server/app.go` (same commit)

- `dbPath string` → `dbPaths database.DatabasePaths` in App struct.
- `app.dbPath, ...` → `app.dbPaths, ...` in `setDB()`, `InitForUnlock()`, `InitForIncrementETag()`.
- `app.dbPath` → `app.dbPaths` in `reconfigurePoolsFromConfig()` call to `RecreatePoolsWithConfig`.
- Any `slog` calls logging `app.dbPath` → `app.dbPaths.Main`.

Then run:

```bash
make format
make lint
go build -o /dev/null ./...
```

Commit message: `database,server: introduce DatabasePaths, wire thumbs.db migration and ATTACH`

---

## ✅ Step 8 — Validate Step 7 (Gemini Flash 3.0) — COMPLETE

- Confirm `DatabasePaths` struct exists with `Main` and `Thumbs` fields.
- Confirm `migrateBlobsDB` uses `migrations.NewThumbsMigrator`.
- Confirm `createDatabasePools` accepts and passes `thumbsDBPath`.
- Confirm `RecreatePoolsWithConfig` accepts `DatabasePaths`.
- Confirm `dbPath string` is gone from App struct; `dbPaths database.DatabasePaths` exists.
- Confirm all four callers of `database.Setup()` in `server/app.go` use `app.dbPaths`.
- Confirm `go build -o /dev/null ./...` succeeds with **no** errors.
- Confirm `make lint` passes.

---

## ✅ Step 9 — custom.sql.go: Write Failing Tests, Then Qualify SQL (Claude Sonnet 4.6) — COMPLETE

### 9a — Write failing tests (RED)

`custom.sql.go` SQL constants currently use bare `thumbnail_blobs`. Tests that call
`GetThumbnailBlobDataByID`, `UpsertThumbnailBlob`, or `GetFolderViewThumbnailBlobDataByPath`
via a DB **without** `thumbs` ATTACHed will succeed today but must fail when the constants
use `thumbs.thumbnail_blobs` and no ATTACH is present.

In `internal/gallerydb/gallerydb_integration_test.go`, add a dedicated test using a connection
that has `thumbs.thumbnail_blobs` available (via ATTACH from a temp thumbs.db with migration
applied). This test verifies the qualified SQL works:

```go
func TestCustomQueriesThumbsDB(t *testing.T) {
    // This test requires thumbs.db to be ATTACHed as "thumbs".
    // setupTestDB must provide that attachment.
    _, q, ctx := setupTestDB(t) // setupTestDB will be updated in Step 9b

    // Insert a thumbnail row (required for FK-like integrity even without FK)
    thumbID, err := q.UpsertThumbnailReturningID(ctx, UpsertThumbnailReturningIDParams{
        FileID: 1,
        Size:   150,
        Width:  150,
        Height: 150,
    })
    if err != nil {
        t.Skipf("cannot create thumbnail row (no file): %v", err)
    }

    blobData := []byte("fake-jpeg-data")
    err = q.(*CustomQueries).UpsertThumbnailBlob(ctx, UpsertThumbnailBlobParams{
        ThumbnailID: thumbID,
        Data:        blobData,
    })
    if err != nil {
        t.Fatalf("UpsertThumbnailBlob: %v", err)
    }

    got, err := q.(*CustomQueries).GetThumbnailBlobDataByID(ctx, thumbID)
    if err != nil {
        t.Fatalf("GetThumbnailBlobDataByID: %v", err)
    }
    if string(got) != string(blobData) {
        t.Errorf("blob mismatch: got %q, want %q", got, blobData)
    }
}
```

Also update `setupTestDB` to ATTACH a temp thumbs.db:

```go
// After applying main migrations, create and ATTACH thumbs.db
thumbsDBPath := filepath.Join(t.TempDir(), "thumbs.db")
thumbsMigrator, err := migrations.NewThumbsMigrator(thumbsDBPath)
if err != nil {
    db.Close()
    t.Fatalf("create thumbs migrator: %v", err)
}
if upErr := thumbsMigrator.Up(); upErr != nil && upErr != migrate.ErrNoChange {
    db.Close()
    t.Fatalf("thumbs migrate up: %v", upErr)
}
thumbsMigrator.Close()
if _, err = db.ExecContext(context.Background(),
    fmt.Sprintf("ATTACH DATABASE 'file:%s' AS thumbs", filepath.ToSlash(thumbsDBPath))); err != nil {
    db.Close()
    t.Fatalf("attach thumbs: %v", err)
}
```

Note: `setupTestDB` uses `:memory:` which may not support ATTACH to a file DB. If ATTACH fails,
switch the main DB to a temp file DB:

```go
tempDir := t.TempDir()
mainDBPath := filepath.Join(tempDir, "test.db")
db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(mainDBPath))
```

Confirm tests **fail** now (because SQL still uses bare `thumbnail_blobs`, no `thumbs` schema on
the `:memory:` DB currently, so the test will fail or error):

```bash
mkdir -p tmp && go test -tags integration ./internal/gallerydb/... > ./tmp/test_output.txt 2>&1
grep -E "FAIL|ERROR" ./tmp/test_output.txt
```

### 9b — Implement (GREEN)

In `internal/gallerydb/custom.sql.go`, update three SQL constant strings:

**`getThumbnailBlobDataByID`**:

```sql
SELECT data
  FROM thumbs.thumbnail_blobs
 WHERE thumbnail_id = ?
```

**`upsertThumbnailBlob`**:

```sql
INSERT INTO thumbs.thumbnail_blobs (thumbnail_id, data)
VALUES (?, ?)
    ON CONFLICT(thumbnail_id)
    DO UPDATE SET data = excluded.data
            WHERE data IS NOT excluded.data
```

**`getFolderViewThumbnailBlobDataByPath`** — change the JOIN line only:

```sql
       INNER JOIN thumbs.thumbnail_blobs tb
               ON f.tile_id = tb.thumbnail_id
```

Then run:

```bash
make format
go build -o /dev/null ./...
mkdir -p tmp && go test -tags integration ./internal/gallerydb/... > ./tmp/test_output.txt 2>&1
grep -E "FAIL|ERROR" ./tmp/test_output.txt
```

All must pass.

Commit message: `gallerydb: qualify thumbnail_blobs queries with thumbs. schema prefix`

---

## ✅ Step 10 — Validate Step 9 (Gemini Flash 3.0) — COMPLETE

- Confirm all three constants use `thumbs.thumbnail_blobs`.
- Confirm no bare `thumbnail_blobs` remains anywhere in `custom.sql.go`.
- Confirm `go build -o /dev/null ./...` succeeds.
- Run: `mkdir -p tmp && go test -tags integration ./internal/gallerydb/... > ./tmp/test_output.txt 2>&1`
- `grep -E "FAIL|ERROR" ./tmp/test_output.txt` must be empty.
- Confirm `TestCustomQueriesThumbsDB` appears as PASS.

---

## Step 11 — Update Remaining Test Helpers (Claude Sonnet 4.6)

### ⚠️ BLOCKED — Architectural Violation Found + Recovery Required

**What happened:**
During Step 11 execution, the agent discovered that `DB().BeginTx()` was being called directly
in three places, bypassing the connection pool (and therefore bypassing the thumbs ATTACH logic):

| Location                                                              | Line | Status               |
| --------------------------------------------------------------------- | ---- | -------------------- |
| `internal/server/app.go` — writeBatcher init                          | ~332 | **PRE-EXISTING**     |
| `internal/server/app.go` — writeBatcher re-init in reconfigure        | ~622 | **PRE-EXISTING**     |
| `internal/server/files/files_integration_test.go` — TestWriteFileInTx | ~769 | **WRITTEN BY AGENT** |

The agent also began partially updating test helpers (`cachelite`, `server/config`,
`server/files`) with thumbs.db setup but used the wrong transaction pattern. **This uncommitted
work is invalid and must be discarded.**

**Architectural rule violated:**

> `DB().BeginTx()` must NEVER be called. ALL database access MUST use connections from
> `dbconnpool` pool (`pool.Get()` / `pool.Put()`). This ensures every connection has the
> thumbs ATTACH applied before any SQL executes.

---

### Recovery Steps (execute before resuming Step 11)

**Pre-existing dirty files that must be discarded (Step 11 partial work):**

- `internal/cachelite/http_cache_middleware_internal_test.go`
- `internal/cachelite/http_cache_middleware_test.go`
- `internal/server/config/config_test.go`
- `internal/server/files/service_test.go`

**Pre-existing dirty files NOT related to Step 11 (keep):**

- `.gitignore`
- `internal/gallerydb/db.go`
- `internal/gallerydb/querier.go`
- `internal/gallerydb/thumbnails.sql.go`
- `scripts/curl_pprof.md`
- `sqlc/queries/thumbnails.sql`
- `version.go`

#### Phase 1 — Save and Reset

```bash
# 1. Create WIP branch at HEAD to preserve all 5 completed commits
git checkout -b refactor-thumbnail-blobs-wip

# 2. Commit the dirty Step 11 partial work onto the WIP branch (don't lose it)
#    Stage ONLY the four Step-11 test files that need to be discarded
git add internal/cachelite/http_cache_middleware_internal_test.go
git add internal/cachelite/http_cache_middleware_test.go
git add internal/server/config/config_test.go
git add internal/server/files/service_test.go
git commit -m "wip: partial step-11 work (invalid DB().BeginTx usage — DO NOT MERGE)"

# 3. Switch back to main and hard-reset to the clean commit
git checkout main
git reset --hard 11187798ce2a86f71b31d723825906ac2176b9c3
```

#### Phase 2 — Fix the Architecture (on main from 11187798) — COMPLETE

**Note:** Phase 2 runs before Steps 1–9. At 11187798, thumbs.db migrations and ATTACH do not exist
yet. Phase 2 fixes the `DB().BeginTx()` violation only — no ATTACH, no ThumbsDBPath.

The `DB().BeginTx()` violations in `app.go` must be fixed so that transactions use a connection
acquired from the pool via `pool.Get()`, then `conn.Conn.BeginTx()`. The connection must not be
returned to the pool until the transaction commits or rolls back (or, for WriteBatcher, the
batcher may hold a dedicated connection for its lifecycle).

High-level changes needed:

1. Replace both `app.dbRwPool.DB().BeginTx(ctx, nil)` calls in `app.go` with `pool.Get()` +
   `conn.Conn.BeginTx()`. (A separate `pool.BeginTx()` helper is optional; direct Get + BeginTx
   is sufficient.)
2. Fix the writeBatcher `BeginTx` callback to use the pooled connection, not `pool.DB().BeginTx()`.
3. Write tests verifying transactions use pooled connections (e.g. `rwPool.Get()` +
   `connRW.Conn.BeginTx()` in `files_integration_test.go`). Do **not** require thumbs ATTACH
   tests here — migrations and ATTACH are added in Steps 3 and 5 (Phase 3).
4. Build and lint must be clean.
5. Full test suite must pass.

Commit message: `fix: use pooled connections for transactions (remove DB().BeginTx usage)`

#### Phase 3 — Cherry-pick the 5 Completed Steps onto Fixed main

After the architecture fix is committed, cherry-pick the 5 steps in order:

```bash
# These SHAs are from the refactor-thumbnail-blobs-wip branch
git cherry-pick a3cfaaf # Step 1: migrations: add 012 to drop thumbnail_blobs from sfpg.db
git cherry-pick bd5066a # Step 3: migrations: add thumbs.db 012 migration and NewThumbsMigrator
git cherry-pick 2cc8410 # Step 5: dbconnpool: add ThumbsDBPath config field and per-connection ATTACH
git cherry-pick 860eb86 # Step 7: database,server: introduce DatabasePaths, wire thumbs.db migration and ATTACH
git cherry-pick c991c95 # Step 9: gallerydb: qualify thumbnail_blobs queries with thumbs. schema prefix
```

**Expected conflicts:**

- `860eb86` touches `server/app.go` — if the architecture fix also modifies `app.go`, resolve
  the conflict keeping DatabasePaths changes AND the new `BeginTx()` calls.
- `2cc8410` touches `dbconnpool.go` — if the fix adds a `BeginTx()` method there, resolve by
  keeping both the ThumbsDBPath/ATTACH changes AND the new BeginTx method.

After each cherry-pick: `go build -o /dev/null ./...` must pass before continuing.

#### Phase 4 — Resume Step 11 Correctly

Once cherry-picks are clean, continue with Step 11 as originally specified but WITHOUT any
`DB().BeginTx()` usage. All test helpers must use `pool.Get()` / `pool.Put()` exclusively.

For `TestWriteFileInTx` specifically — it must be rewritten to use a pooled RW connection
for the transaction rather than `rwPool.DB().BeginTx()`.

---

### Step 11 Implementation (execute after Recovery)

Any remaining test helpers that create pools without `ThumbsDBPath` will fail when they try to
prepare `thumbs.thumbnail_blobs` statements. Find and fix all of them.

Key files to check:

- `internal/server/files/service_test.go` — `createTestPoolsAndDir`
- `internal/cachelite/http_cache_middleware_test.go` — `createTestDBPool`
- `internal/cachelite/http_cache_middleware_internal_test.go` — `createTestDBPoolTB`
- `internal/server/config/config_test.go` — `createTestService`

For each such helper:

1. Add thumbs.db creation + migration (same pattern as Step 9a).
2. Add `ThumbsDBPath: thumbsDBPath` to the pool Config.
3. Do NOT use `DB().BeginTx()` anywhere.

For `TestWriteFileInTx` in `files_integration_test.go`:

- Rewrite to acquire a pooled connection via `rwPool.Get()`, use the connection's transaction
  capability or the new `pool.BeginTx()` method.

Then run the full test suite:

```bash
make format
make lint
mkdir -p tmp
go test -tags "integration e2e" ./... > ./tmp/test_output.txt 2>&1
grep -E "FAIL|ERROR" ./tmp/test_output.txt
```

Must be empty.

Commit message: `tests: update all pool-creating test helpers to wire thumbs.db`

---

## Step 12 — Full Validation (Claude Sonnet 4.6)

- Run: `mkdir -p tmp && go test -tags "integration e2e" ./... > ./tmp/test_output.txt 2>&1`
- `grep -E "FAIL|ERROR" ./tmp/test_output.txt` must be empty.
- Confirm `make lint` passes.
- Confirm `make format-check` passes.
- Confirm `go build -o /dev/null ./...` is clean.
- Run curl smoke test against the running dev server (port 8083):

```bash
# Verify server is running
curl -s http://localhost:8083/gallery/1 | head -5

# Thumbnail routes are public — no authentication required
curl -s -o /dev/null -w "%{http_code}" http://localhost:8083/thumbnail/file/1
curl -s -o /dev/null -w "%{http_code}" http://localhost:8083/thumbnail/folder/1
```

If any check fails, correct in place and trigger an additional validation sub-agent.

---

## Dependency Graph

```
1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9 → 10 → 11 → 12
```

- Steps 1–2: sfpg.db migration SQL files (no Go changes, build always green)
- Steps 3–4: thumbs.db migration + `NewThumbsMigrator` — test first, then implement
- Steps 5–6: `ThumbsDBPath` + ATTACH in dbconnpool — test first, then implement
- Step 7–8: `DatabasePaths` in `database/app.go` AND `dbPaths` in `server/app.go` in one commit (keeps build green)
- Steps 9–10: SQL qualification in `custom.sql.go` — test first, then implement
- Steps 11–12: Remaining test helpers + full sweep validation
