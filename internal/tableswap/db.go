// Package tableswap provides primitives for atomic SQLite table rotations:
// CloneEmpty, CreateIndexes, and Swap.
//
// In this version, triggers and views are unsupported. CloneEmpty copies only
// the table definition (sqlite_master type='table') and rewrites the table
// identifier. Triggers and views on the active table are not cloned.
//
// Identifier rewrite is whole-word (\b), not SQL-role-aware. A table whose
// CREATE statement uses the table name as another word token (column, literal,
// or unquoted index name equal to the table) will rewrite those tokens too.
// The intended consumers http_cache and file_folder_index do not do that;
// consumer_ddl_lock_integration_test.go locks their DDL.
//
// Concurrency note: production Swap DROP TABLEs the stale table on the same
// goroutine before returning. Tests using staleDropHold still spawn dropStale;
// those tests use SetMaxOpenConns(1) and hold-before-BeginTx.
//
// Index names: SQLite index names are database-global. CreateIndexes allocates
// a free name with allocateIndexName. The first copy while the live index still
// exists therefore lands on dest as idx_<base>_1, not idx_<base>. After Swap,
// the live table keeps that suffixed name. Callers must not assume the
// unsuffixed name. A later CloneEmpty+CreateIndexes reuses idx_<base> only
// after the stale table (and its indexes) are gone. Do not start a second
// rotate while *_to_be_dropped still exists if base-name reuse is required.
package tableswap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

// DB is the subset of *sql.DB methods needed by tableswap operations.
// *sql.DB satisfies this interface.
type DB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// staleDropMode selects how the stale-table drop runs after a successful Swap
// cutover. It is a package-level test seam (default: drop before Swap returns).
//
//   - staleDropSync (default, production): DROP TABLE {active}_to_be_dropped
//     runs on the Swap goroutine after commit; Swap returns after DROP and Put.
//   - staleDropSkip: dropStale is not run, so t_to_be_dropped lingers.
//     Cutover/leftover tests use this so they can inspect t_to_be_dropped
//     without racing the DROP.
//   - staleDropHold: dropStale is spawned but its worker blocks BEFORE it
//     acquires a DB connection or runs DROP (it must not skip DROP). Hold-seam
//     tests use this to prove Swap returns before DROP and DROP later runs.
//     Hold-before-BeginTx is mandatory for tests that use SetMaxOpenConns(1):
//     if the worker held a connection while blocked, post-Swap queries would hang.
//
// Every test that mutates staleDrop MUST t.Cleanup back to staleDropSync.
// The seam is unexported. staleDrop is an int so accidental string/bool
// assignments are rejected by the compiler.
type staleDropMode int

const (
	staleDropSync staleDropMode = iota // default: DROP TABLE then return
	staleDropSkip                      // do not DROP TABLE after cutover
	staleDropHold                      // test only: spawn, block worker before BeginTx
)

// dropHold gates the staleDropHold seam. While held, the spawned dropStale
// worker blocks before it acquires a DB connection or runs DROP. release
// lets the worker proceed. It is nil unless staleDrop == staleDropHold.
//
// release is idempotent: it closes the released channel at most once, so it
// is safe to call multiple times (e.g., from both a test body and t.Cleanup)
// without panicking on a double close.
type dropHoldGate struct {
	released chan struct{}
	once     sync.Once
}

// newDropHold returns a fresh hold gate (blocked).
func newDropHold() *dropHoldGate {
	return &dropHoldGate{released: make(chan struct{})}
}

// release unblocks the worker. It is safe to call more than once.
func (g *dropHoldGate) release() { g.once.Do(func() { close(g.released) }) }

// waitForHold blocks until release is called. A nil gate returns immediately.
func (g *dropHoldGate) waitForHold() {
	if g == nil {
		return
	}
	<-g.released
}

var staleDrop staleDropMode = staleDropSync
var dropHold *dropHoldGate

// rewriteIdentifier performs an identifier-bounded substitution: it replaces
// every whole-word occurrence of from with to in query. Word boundaries (\b)
// are matched so that partial matches — for instance table "t" matching the
// "t" inside "t_new" — are left untouched. from is escaped via
// regexp.QuoteMeta so its characters are treated literally.
func rewriteIdentifier(query, from, to string) string {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(from) + `\b`)
	return re.ReplaceAllString(query, to)
}

// CloneEmpty creates an empty destination table ({active}_new) by cloning the
// schema of the active table without copying data. It runs in a single
// transaction: drops any existing destination, reads the active table's CREATE
// statement from sqlite_master (type='table'), rewrites the table identifier to
// the destination name (identifier-bounded), and executes it.
//
// In this version, triggers and views are unsupported: only the table
// definition is cloned, so the destination has no triggers or views.
func CloneEmpty(ctx context.Context, db DB, active string) error {
	dest := destName(active)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			err = fmt.Errorf("rollback: %w", rbErr)
		}
	}()

	// Drop any leftover destination table.
	if _, execErr := tx.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", dest)); execErr != nil {
		return fmt.Errorf("drop leftover %s: %w", dest, execErr)
	}

	// Read the active table's CREATE statement from sqlite_master (type='table').
	var createSQL string
	err = tx.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type='table' AND name=?", active).Scan(&createSQL)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("active table %q does not exist", active)
		}
		return err
	}

	// Rewrite the table identifier (identifier-bounded, not a blind ReplaceAll
	// of the active name through the SQL text).
	rewritten := rewriteIdentifier(createSQL, active, dest)

	if _, execErr := tx.ExecContext(ctx, rewritten); execErr != nil {
		return execErr
	}

	return tx.Commit()
}

// logDestPhase emits the matching start/finish Debug line for CreateIndexes and
// Swap: "{dest} {phase} completed successfully" or "{dest} {phase} failed".
func logDestPhase(dest, phase string, started time.Time, err error) {
	elapsed := time.Since(started)
	if err != nil {
		slog.Debug(dest+" "+phase+" failed", "err", err, "elapsed", elapsed)
		return
	}
	slog.Debug(dest+" "+phase+" completed successfully", "elapsed", elapsed)
}

// CreateIndexes copies every explicit (non-sqlite_autoindex) index defined on
// the active table onto the destination table ({active}_new). It runs in a
// single transaction: first snapshots the OCCUPANCY set (every index name in
// sqlite_master across all tables, plus names allocated in this run), then
// snapshots the COPY set (explicit indexes with tbl_name = active only). Each
// COPY-set index is allocated a non-colliding name via allocateIndexName, its
// CREATE INDEX SQL is rewritten with exactly two identifier-bounded
// substitutions (the index name → allocated name; ON <active> → ON <dest>),
// and the rewritten statement is executed on the destination. The active table
// must already have a destination ({active}_new); CreateIndexes returns an
// error if it is missing.
func CreateIndexes(ctx context.Context, db DB, active string) (err error) {
	dest := destName(active)
	started := time.Now()
	slog.Debug(dest + " create index starting")
	defer func() { logDestPhase(dest, "create index", started, err) }()

	// Save current temp_store, set FILE so the index sorter spills to disk
	// instead of OOMing the wasm heap on large tables.
	var saved int
	if err = db.QueryRowContext(ctx, "PRAGMA temp_store").Scan(&saved); err != nil {
		return fmt.Errorf("read temp_store: %w", err)
	}
	if _, execErr := db.ExecContext(ctx, "PRAGMA temp_store=FILE"); execErr != nil {
		return fmt.Errorf("set temp_store=FILE: %w", execErr)
	}
	defer func() {
		if _, rerr := db.ExecContext(context.WithoutCancel(ctx), "PRAGMA temp_store="+strconv.Itoa(saved)); rerr != nil {
			slog.Error("tableswap restore temp_store failed", "err", rerr, "saved", saved)
			if err == nil {
				err = fmt.Errorf("restore temp_store: %w", rerr)
			}
		}
	}()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			if err == nil {
				err = fmt.Errorf("rollback: %w", rbErr)
			}
		}
	}()

	// The destination table must already exist (created by CloneEmpty).
	var destExists bool
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) > 0 FROM sqlite_master WHERE type='table' AND name=?", dest).Scan(&destExists); err != nil {
		return fmt.Errorf("check destination table %q: %w", dest, err)
	}
	if !destExists {
		return fmt.Errorf("destination table %q does not exist; CloneEmpty must run first", dest)
	}

	// Snapshot the OCCUPANCY set: every index name in sqlite_master (all
	// tables), excluding sqlite_autoindex_* and non-index types. Read every
	// row and close the query before issuing any CREATE INDEX.
	occupancy := make(map[string]bool)
	occRows, err := tx.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type='index' AND name NOT LIKE 'sqlite_autoindex_%'")
	if err != nil {
		return fmt.Errorf("snapshot occupancy set: %w", err)
	}
	for occRows.Next() {
		var name string
		if err = occRows.Scan(&name); err != nil {
			occRows.Close()
			return fmt.Errorf("scan occupancy index name: %w", err)
		}
		occupancy[name] = true
	}
	if err = occRows.Err(); err != nil {
		occRows.Close()
		return fmt.Errorf("iterate occupancy set: %w", err)
	}
	occRows.Close()

	// Snapshot the COPY set: explicit indexes whose tbl_name is the active
	// table only. Other tables' index SQL is never copied.
	type indexDef struct {
		name string
		sql  string
	}
	var copySet []indexDef
	copyRows, err := tx.QueryContext(ctx, "SELECT name, sql FROM sqlite_master WHERE type='index' AND tbl_name=? AND name NOT LIKE 'sqlite_autoindex_%'", active)
	if err != nil {
		return fmt.Errorf("snapshot copy set: %w", err)
	}
	for copyRows.Next() {
		var d indexDef
		if err = copyRows.Scan(&d.name, &d.sql); err != nil {
			copyRows.Close()
			return fmt.Errorf("scan copy-set index: %w", err)
		}
		copySet = append(copySet, d)
	}
	if err = copyRows.Err(); err != nil {
		copyRows.Close()
		return fmt.Errorf("iterate copy set: %w", err)
	}
	copyRows.Close()

	// Allocate a non-colliding name for each COPY-set index, then CREATE it
	// on the destination. OCCUPANCY grows with each allocation so two source
	// indexes that strip to the same base cannot collide.
	for _, idx := range copySet {
		existing := make([]string, 0, len(occupancy))
		for name := range occupancy {
			existing = append(existing, name)
		}
		newName := allocateIndexName(idx.name, existing)
		occupancy[newName] = true

		// Rewrite CREATE INDEX SQL with exactly two identifier-bounded
		// substitutions (not a blind ReplaceAll of the table name):
		// (1) the index identifier → allocated name (SQLite index names are
		//     DB-global; leaving the original name collides);
		// (2) ON <active> → ON <dest>.
		rewritten := rewriteIdentifier(idx.sql, idx.name, newName)
		rewritten = rewriteIdentifier(rewritten, active, dest)

		if _, err = tx.ExecContext(ctx, rewritten); err != nil {
			return fmt.Errorf("create index %q on %s: %w", newName, dest, err)
		}
	}

	return tx.Commit()
}

// Swap atomically replaces the live table with {active}_new. Inside one
// transaction it DROP TABLE IF EXISTS {active}_to_be_dropped, renames active
// to that stale name, and renames dest to active. After commit it DROP TABLEs
// the freshly renamed stale table on this goroutine, Puts cpc, and returns.
// It returns an error if the destination table is missing.
//
// cpc is a dedicated *dbconnpool.CpConn leased by the caller; Swap runs SQL
// against cpc.Conn. Callers must not Put cpc. put returns the connection
// (db.Put in production). Swap calls put exactly once: on cutover error, or
// after the post-cutover DROP TABLE.
//
// A DROP TABLE failure after a successful cutover is logged; Swap still
// returns nil. The next Swap's pre-rename DROP IF EXISTS clears a lingering
// stale table so the rename can proceed.
func Swap(ctx context.Context, cpc *dbconnpool.CpConn, put func(*dbconnpool.CpConn), active string) (err error) {
	dest := destName(active)
	stale := staleName(active)
	started := time.Now()
	slog.Debug(dest + " tableswap starting")
	defer func() { logDestPhase(dest, "tableswap", started, err) }()

	// putOnce releases the leased connection exactly once, regardless of which
	// code path performs the Put. Swap never defers put at the top: skip Puts
	// on the way out; production DROP Puts after DROP TABLE; hold Puts in the
	// spawned worker.
	var putOnce sync.Once
	putOnceFn := func() { putOnce.Do(func() { put(cpc) }) }

	tx, err := cpc.Conn.BeginTx(ctx, nil)
	if err != nil {
		putOnceFn()
		return err
	}

	// rollback aborts the cutover transaction. It MUST run before putOnceFn on
	// every error path: put closes (or returns) the connection, and rolling
	// back an already-released connection would deadlock. The transaction is
	// already done (committed or rolled back) on the success / drop-spawn
	// paths, so a stray rollback there is a harmless no-op.
	rollback := func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			slog.Error("tableswap Swap rollback failed", "err", rbErr, "active", active)
		}
	}

	// Drop any prior stale table ({active}_to_be_dropped) before the cutover.
	// This is part of the atomic cutover: a leftover from a prior Swap must be
	// removed so the active table can be renamed into the stale name. DROP IF
	// EXISTS never errors when absent, so this is unconditional and runs inside
	// the transaction regardless of staleDrop (which only gates the post-commit
	// DROP TABLE of the freshly renamed stale table, not this pre-rename cleanup).
	if _, err = tx.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", stale)); err != nil {
		rollback()
		putOnceFn()
		return fmt.Errorf("drop prior stale %s: %w", stale, err)
	}

	// Both renames happen in one transaction so the cutover is atomic.
	if _, err = tx.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s RENAME TO %s", active, stale)); err != nil {
		rollback()
		putOnceFn()
		return err
	}
	if _, err = tx.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s RENAME TO %s", dest, active)); err != nil {
		rollback()
		putOnceFn()
		return err
	}

	if err = tx.Commit(); err != nil {
		rollback()
		putOnceFn()
		return err
	}

	if staleDrop == staleDropSkip {
		putOnceFn()
		return nil
	}
	if staleDrop == staleDropHold {
		go dropStale(ctx, cpc, stale, putOnceFn)
		return nil
	}
	dropStale(ctx, cpc, stale, putOnceFn)
	return nil
}

// dropStale DROP TABLEs the stale table in its own transaction (not the
// cutover transaction) using a detached context so cancellation of the
// caller's context does not abort an in-flight DROP TABLE. On BeginTx, Exec,
// or Commit failure it logs via slog; Swap still returns nil after a
// successful cutover even if this DROP TABLE later fails. The drop is
// best-effort: a missed DROP TABLE IF EXISTS leaves the stale table lingering.
//
// Production (staleDropSync) calls this on the Swap goroutine. staleDropHold
// calls it from a spawned goroutine and blocks here before BeginTx so tests
// using SetMaxOpenConns(1) can observe the table; holding a connection while
// blocked would hang post-Swap queries.
func dropStale(ctx context.Context, cpc *dbconnpool.CpConn, stale string, put func()) {
	dropCtx := context.WithoutCancel(ctx)

	if staleDrop == staleDropHold && dropHold != nil {
		dropHold.waitForHold()
	}

	tx, err := cpc.Conn.BeginTx(dropCtx, nil)
	if err != nil {
		slog.Error("tableswap dropStale begin failed", "err", err, "stale", stale)
		put()
		return
	}
	defer func() {
		if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
			slog.Error("tableswap dropStale rollback failed", "err", rbErr, "stale", stale)
		}
	}()

	if _, err = tx.ExecContext(dropCtx, fmt.Sprintf("DROP TABLE IF EXISTS %s", stale)); err != nil {
		slog.Error("tableswap dropStale exec failed", "err", err, "stale", stale)
		put()
		return
	}
	if err = tx.Commit(); err != nil {
		slog.Error("tableswap dropStale commit failed", "err", err, "stale", stale)
		put()
		return
	}
	put()
}
