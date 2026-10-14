package dbconnpool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/migrations"
)

func memTestConfig(ro, withThumbs bool) Config {
	cfg := Config{
		DriverName:         "sqlite3",
		MaxConnections:     2,
		MinIdleConnections: 0,
		ReadOnly:           ro,
		QueriesFunc:        gallerydb.NewCustomQueries,
		TestUseMemDB:       true,
	}
	if withThumbs {
		// CustomQueries prepares thumbs.thumbnail_blobs statements per
		// connection, so thumbs-attached pools need a thumbs source.
		cfg.ThumbsDBPath = "thumbs.db"
	}
	return cfg
}

func openMemPool(t *testing.T, ctx context.Context, ro, withThumbs bool) *DbSQLConnPool {
	t.Helper()
	pool, err := NewDbSQLConnPool(ctx, "", memTestConfig(ro, withThumbs))
	if err != nil {
		t.Fatalf("NewDbSQLConnPool(ro=%v): %v", ro, err)
	}
	return pool
}

const isolatedEnsureSchemaKey = "dbconnpool-ensure-isolated"

func currentSchemaKey(t *testing.T) string {
	t.Helper()
	key, err := migrations.LatestSchemaKey()
	if err != nil {
		t.Fatalf("LatestSchemaKey: %v", err)
	}
	return key
}

func memSlot(t *testing.T, key string) *memDBSlot {
	t.Helper()
	v, ok := memMainSlots.Load(key)
	if !ok {
		t.Fatalf("no mem slot for schema key %q", key)
	}
	s, ok := v.(*memDBSlot)
	if !ok || s == nil {
		t.Fatalf("bad mem slot for schema key %q", key)
	}
	return s
}

func mustExec(t *testing.T, ctx context.Context, conn *sql.Conn, query string, args ...any) {
	t.Helper()
	if _, err := conn.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func mustQueryText(t *testing.T, ctx context.Context, conn *sql.Conn, query string, args ...any) string {
	t.Helper()
	var out string
	if err := conn.QueryRowContext(ctx, query, args...).Scan(&out); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	return out
}

func assertMemTablePresent(t *testing.T, ctx context.Context, conn *sql.Conn, db, table string) {
	t.Helper()
	q := fmt.Sprintf("SELECT name FROM %s.sqlite_master WHERE type='table' AND name=? LIMIT 1", db)
	var name string
	if err := conn.QueryRowContext(ctx, q, table).Scan(&name); err != nil {
		t.Fatalf("%s.%s not present: %v", db, table, err)
	}
}

func TestMemDBFlagFalseEmptyDSNIsNotMemory(t *testing.T) {
	ctx := context.Background()
	cfg := Config{
		DriverName:         "sqlite3",
		MaxConnections:     1,
		MinIdleConnections: 0,
		QueriesFunc:        gallerydb.NewCustomQueries,
	}
	pool, err := NewDbSQLConnPool(ctx, "", cfg)
	if err != nil {
		t.Fatalf("empty DSN without TestUseMemDB: %v", err)
	}
	defer pool.Close()
	if pool.isMemory {
		t.Fatal("empty DSN without TestUseMemDB opened memory-VFS mode")
	}
}

func TestMemDBFlagTrueEmptyDSNIsMemory(t *testing.T) {
	ctx := context.Background()
	cfg := Config{
		DriverName:         "sqlite3",
		MaxConnections:     1,
		MinIdleConnections: 0,
		QueriesFunc:        gallerydb.NewCustomQueries,
		TestUseMemDB:       true,
	}
	pool, err := NewDbSQLConnPool(ctx, "", cfg)
	if err != nil {
		t.Fatalf("empty DSN with TestUseMemDB: %v", err)
	}
	defer pool.Close()
	if !pool.isMemory {
		t.Fatal("empty DSN with TestUseMemDB did not open memory-VFS mode")
	}
}

func TestMemDBSharedAcrossDistinctPools(t *testing.T) {
	ctx := context.Background()

	rw1 := openMemPool(t, ctx, false, true)
	defer rw1.Close()

	c1, err := rw1.Get()
	if err != nil {
		t.Fatalf("rw1 get conn1: %v", err)
	}
	c2, err := rw1.Get()
	if err != nil {
		rw1.Put(c1)
		t.Fatalf("rw1 get conn2: %v", err)
	}
	if c1.Conn == c2.Conn {
		rw1.Put(c2)
		rw1.Put(c1)
		t.Fatal("expected two distinct physical connections")
	}

	const mainTable = "mem_shared_main"
	const thumbsTable = "mem_shared_thumbs"
	mustExec(t, ctx, c1.Conn, `CREATE TABLE IF NOT EXISTS `+mainTable+` (id INTEGER PRIMARY KEY, v TEXT)`)
	mustExec(t, ctx, c1.Conn, `INSERT INTO `+mainTable+` (id, v) VALUES (1, 'main-one')`)
	mustExec(t, ctx, c1.Conn, `CREATE TABLE thumbs.`+thumbsTable+` (id INTEGER PRIMARY KEY, v TEXT)`)
	mustExec(t, ctx, c1.Conn, `INSERT INTO thumbs.`+thumbsTable+` (id, v) VALUES (1, 'thumbs-one')`)
	if got := mustQueryText(t, ctx, c2.Conn, `SELECT v FROM `+mainTable+` WHERE id = 1`); got != "main-one" {
		rw1.Put(c2)
		rw1.Put(c1)
		t.Fatalf("conn2 main row = %q, want %q", got, "main-one")
	}
	if got := mustQueryText(t, ctx, c2.Conn, `SELECT v FROM thumbs.`+thumbsTable+` WHERE id = 1`); got != "thumbs-one" {
		rw1.Put(c2)
		rw1.Put(c1)
		t.Fatalf("conn2 thumbs row = %q, want %q", got, "thumbs-one")
	}

	rw2 := openMemPool(t, ctx, false, true)
	defer rw2.Close()
	if rw2.DB() == rw1.DB() {
		rw1.Put(c2)
		rw1.Put(c1)
		t.Fatal("second RW pool shares *sql.DB with first pool")
	}
	c3, err := rw2.Get()
	if err != nil {
		rw1.Put(c2)
		rw1.Put(c1)
		t.Fatalf("rw2 get: %v", err)
	}
	defer rw2.Put(c3)
	if got := mustQueryText(t, ctx, c3.Conn, `SELECT v FROM `+mainTable+` WHERE id = 1`); got != "main-one" {
		rw1.Put(c2)
		rw1.Put(c1)
		t.Fatalf("rw2 main row = %q, want %q", got, "main-one")
	}
	if got := mustQueryText(t, ctx, c3.Conn, `SELECT v FROM thumbs.`+thumbsTable+` WHERE id = 1`); got != "thumbs-one" {
		rw1.Put(c2)
		rw1.Put(c1)
		t.Fatalf("rw2 thumbs row = %q, want %q", got, "thumbs-one")
	}

	ro := openMemPool(t, ctx, true, true)
	defer ro.Close()
	if ro.DB() == rw1.DB() || ro.DB() == rw2.DB() {
		rw1.Put(c2)
		rw1.Put(c1)
		t.Fatal("RO pool shares *sql.DB with an RW pool")
	}
	c4, err := ro.Get()
	if err != nil {
		rw1.Put(c2)
		rw1.Put(c1)
		t.Fatalf("ro get: %v", err)
	}
	defer ro.Put(c4)
	if got := mustQueryText(t, ctx, c4.Conn, `SELECT v FROM `+mainTable+` WHERE id = 1`); got != "main-one" {
		rw1.Put(c2)
		rw1.Put(c1)
		t.Fatalf("ro main row = %q, want %q", got, "main-one")
	}
	if got := mustQueryText(t, ctx, c4.Conn, `SELECT v FROM thumbs.`+thumbsTable+` WHERE id = 1`); got != "thumbs-one" {
		rw1.Put(c2)
		rw1.Put(c1)
		t.Fatalf("ro thumbs row = %q, want %q", got, "thumbs-one")
	}

	assertMemTablePresent(t, ctx, c4.Conn, "main", "config")
	assertMemTablePresent(t, ctx, c4.Conn, "thumbs", "thumbnail_blobs")

	rw1.Put(c2)
	rw1.Put(c1)
}

func TestMemDBReadOnlyPoolRejectsWrites(t *testing.T) {
	ctx := context.Background()

	rw := openMemPool(t, ctx, false, true)
	defer rw.Close()
	ro := openMemPool(t, ctx, true, true)
	defer ro.Close()

	rwConn, err := rw.Get()
	if err != nil {
		t.Fatalf("rw get: %v", err)
	}
	defer rw.Put(rwConn)
	mustExec(t, ctx, rwConn.Conn, `CREATE TABLE IF NOT EXISTS mem_ro_main (id INTEGER PRIMARY KEY, v TEXT)`)
	mustExec(t, ctx, rwConn.Conn, `INSERT INTO mem_ro_main (id, v) VALUES (1, 'kept')`)
	mustExec(t, ctx, rwConn.Conn, `CREATE TABLE thumbs.mem_ro_thumbs (id INTEGER PRIMARY KEY, v TEXT)`)
	mustExec(t, ctx, rwConn.Conn, `INSERT INTO thumbs.mem_ro_thumbs (id, v) VALUES (1, 'kept')`)

	roConn, err := ro.Get()
	if err != nil {
		t.Fatalf("ro get: %v", err)
	}
	defer ro.Put(roConn)

	if _, err := roConn.Conn.ExecContext(ctx, `INSERT INTO mem_ro_main (id, v) VALUES (2, 'nope')`); err == nil {
		t.Fatal("expected RO main INSERT to fail")
	}
	if _, err := roConn.Conn.ExecContext(ctx, `INSERT INTO thumbs.mem_ro_thumbs (id, v) VALUES (2, 'nope')`); err == nil {
		t.Fatal("expected RO thumbs INSERT to fail")
	}
	if got := mustQueryText(t, ctx, roConn.Conn, `SELECT v FROM mem_ro_main WHERE id = 1`); got != "kept" {
		t.Fatalf("ro main row = %q, want %q", got, "kept")
	}
	if got := mustQueryText(t, ctx, roConn.Conn, `SELECT v FROM thumbs.mem_ro_thumbs WHERE id = 1`); got != "kept" {
		t.Fatalf("ro thumbs row = %q, want %q", got, "kept")
	}
}

type dbFileEntry struct {
	seq  int
	name string
	file string
}

func listMemDatabaseFiles(t *testing.T, ctx context.Context, conn *sql.Conn) []dbFileEntry {
	t.Helper()
	rows, err := conn.QueryContext(ctx, `SELECT seq, name, file FROM pragma_database_list()`)
	if err != nil {
		t.Fatalf("pragma_database_list: %v", err)
	}
	defer rows.Close()
	var out []dbFileEntry
	for rows.Next() {
		var e dbFileEntry
		if err := rows.Scan(&e.seq, &e.name, &e.file); err != nil {
			t.Fatalf("scan database list: %v", err)
		}
		if e.name == "main" || e.name == "thumbs" {
			out = append(out, e)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("database list entries = %v, want main and thumbs", out)
	}
	return out
}

func TestMemDBBackingsAreNotFilesystemFiles(t *testing.T) {
	ctx := context.Background()
	key := currentSchemaKey(t)

	rw := openMemPool(t, ctx, false, true)
	defer rw.Close()
	ro := openMemPool(t, ctx, true, true)
	defer ro.Close()

	rwConn, err := rw.Get()
	if err != nil {
		t.Fatalf("rw get: %v", err)
	}
	defer rw.Put(rwConn)
	mustExec(t, ctx, rwConn.Conn, `CREATE TABLE IF NOT EXISTS mem_fs_main (id INTEGER PRIMARY KEY)`)
	mustExec(t, ctx, rwConn.Conn, `INSERT INTO mem_fs_main (id) VALUES (1)`)
	mustExec(t, ctx, rwConn.Conn, `CREATE TABLE thumbs.mem_fs_thumbs (id INTEGER PRIMARY KEY)`)
	mustExec(t, ctx, rwConn.Conn, `INSERT INTO thumbs.mem_fs_thumbs (id) VALUES (1)`)

	for _, e := range listMemDatabaseFiles(t, ctx, rwConn.Conn) {
		if strings.Contains(e.file, "sfpg-test-main-") {
			t.Fatalf("%s backing uses temp prefix: %q", e.name, e.file)
		}
		if e.file != "" {
			if _, err := os.Stat(e.file); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%s backing file %q stat err = %v, want ErrNotExist", e.name, e.file, err)
			}
		}
	}
	var mainJournal, thumbsJournal string
	if err := rwConn.Conn.QueryRowContext(ctx, `PRAGMA main.journal_mode`).Scan(&mainJournal); err != nil {
		t.Fatalf("main journal_mode: %v", err)
	}
	if err := rwConn.Conn.QueryRowContext(ctx, `PRAGMA thumbs.journal_mode`).Scan(&thumbsJournal); err != nil {
		t.Fatalf("thumbs journal_mode: %v", err)
	}
	if !strings.EqualFold(mainJournal, "memory") {
		t.Fatalf("main journal_mode = %q, want memory", mainJournal)
	}
	if !strings.EqualFold(thumbsJournal, "memory") {
		t.Fatalf("thumbs journal_mode = %q, want memory", thumbsJournal)
	}

	if err := rw.Close(); err != nil {
		t.Fatalf("rw close: %v", err)
	}
	if err := ro.Close(); err != nil {
		t.Fatalf("ro close: %v", err)
	}
	if _, ok := memMainSlots.Load(key); ok {
		t.Fatalf("slot %q still present after final close", key)
	}
}

func TestMemDBPoolLifetime(t *testing.T) {
	ctx := context.Background()
	key := currentSchemaKey(t)

	// CustomQueries prepares thumbs.thumbnail_blobs statements per connection,
	// so a pool without a thumbs source cannot serve Get(). Drive the
	// main-only pool through its raw *sql.DB handle instead.
	mainOnly := openMemPool(t, ctx, false, false)
	if _, err := mainOnly.DB().ExecContext(ctx, `CREATE TABLE IF NOT EXISTS mem_life_main (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("main-only exec: %v", err)
	}
	if _, err := mainOnly.DB().ExecContext(ctx, `INSERT INTO mem_life_main (id, v) VALUES (1, 'sentinel')`); err != nil {
		t.Fatalf("main-only insert: %v", err)
	}

	withThumbs := openMemPool(t, ctx, false, true)
	c2, err := withThumbs.Get()
	if err != nil {
		t.Fatalf("thumbs-enabled get: %v", err)
	}
	if got := mustQueryText(t, ctx, c2.Conn, `SELECT v FROM mem_life_main WHERE id = 1`); got != "sentinel" {
		withThumbs.Put(c2)
		t.Fatalf("main sentinel after thumbs acquire = %q, want sentinel", got)
	}
	mustExec(t, ctx, c2.Conn, `CREATE TABLE thumbs.mem_life_thumbs (id INTEGER PRIMARY KEY, v TEXT)`)
	mustExec(t, ctx, c2.Conn, `INSERT INTO thumbs.mem_life_thumbs (id, v) VALUES (1, 'sentinel')`)
	withThumbs.Put(c2)

	ro := openMemPool(t, ctx, true, true)
	c3, err := ro.Get()
	if err != nil {
		t.Fatalf("ro get: %v", err)
	}
	if got := mustQueryText(t, ctx, c3.Conn, `SELECT v FROM mem_life_main WHERE id = 1`); got != "sentinel" {
		ro.Put(c3)
		t.Fatalf("ro main sentinel = %q, want sentinel", got)
	}
	if got := mustQueryText(t, ctx, c3.Conn, `SELECT v FROM thumbs.mem_life_thumbs WHERE id = 1`); got != "sentinel" {
		ro.Put(c3)
		t.Fatalf("ro thumbs sentinel = %q, want sentinel", got)
	}
	ro.Put(c3)

	cerr := mainOnly.Close()
	if cerr != nil {
		t.Fatalf("main-only close: %v", cerr)
	}
	cerr = withThumbs.Close()
	if cerr != nil {
		t.Fatalf("thumbs-enabled close: %v", cerr)
	}

	c4, err := ro.Get()
	if err != nil {
		t.Fatalf("ro get after closes: %v", err)
	}
	if got := mustQueryText(t, ctx, c4.Conn, `SELECT v FROM mem_life_main WHERE id = 1`); got != "sentinel" {
		ro.Put(c4)
		t.Fatalf("ro main sentinel after closes = %q, want sentinel", got)
	}
	if got := mustQueryText(t, ctx, c4.Conn, `SELECT v FROM thumbs.mem_life_thumbs WHERE id = 1`); got != "sentinel" {
		ro.Put(c4)
		t.Fatalf("ro thumbs sentinel after closes = %q, want sentinel", got)
	}
	ro.Put(c4)
	cerr = ro.Close()
	if cerr != nil {
		t.Fatalf("ro close: %v", cerr)
	}
	if _, ok := memMainSlots.Load(key); ok {
		t.Fatalf("slot %q still present after final close", key)
	}

	fresh := openMemPool(t, ctx, false, true)
	defer fresh.Close()
	cf, err := fresh.Get()
	if err != nil {
		t.Fatalf("fresh get: %v", err)
	}
	defer fresh.Put(cf)
	var n int
	if err := cf.Conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM mem_life_main`).Scan(&n); err == nil {
		t.Fatalf("stale main sentinel table survived final release (count=%d)", n)
	}
	if err := cf.Conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM thumbs.mem_life_thumbs`).Scan(&n); err == nil {
		t.Fatalf("stale thumbs sentinel table survived final release (count=%d)", n)
	}
	assertMemTablePresent(t, ctx, cf.Conn, "main", "config")
	assertMemTablePresent(t, ctx, cf.Conn, "thumbs", "thumbnail_blobs")
}

// failingThumbsLease wraps the production lease and fails ThumbsURI so
// acquisition rollback is exercised deterministically.
type failingThumbsLease struct {
	memDBLease
}

func (l *failingThumbsLease) ThumbsURI(ctx context.Context, readOnly bool) (string, error) {
	return "", errMemDBLeaseThumbs
}

func withFailingThumbsLease(t *testing.T) {
	t.Helper()
	prior := newMemDBLease
	newMemDBLease = func(ctx context.Context, schemaKey string) (memDBLease, error) {
		lease, err := prior(ctx, schemaKey)
		if err != nil {
			return nil, err
		}
		return &failingThumbsLease{memDBLease: lease}, nil
	}
	t.Cleanup(func() { newMemDBLease = prior })
}

func TestMemDBAcquireRollback(t *testing.T) {
	ctx := context.Background()
	key := currentSchemaKey(t)

	// The injected lease fails ThumbsURI during acquisition, exercising
	// transactional cleanup of a newly created zero-reference slot.
	// Scope the injection with t.Cleanup so the factory default is
	// restored for the successful main-only acquisition below.
	withFailingThumbsLease(t)
	if _, err := openMemTestPoolWithConfig(ctx, memTestConfig(false, true)); err == nil {
		t.Fatal("expected thumbs materialization failure")
	}
	if _, ok := memMainSlots.Load(key); ok {
		t.Fatalf("slot %q present after failed acquisition", key)
	}

	mainOnly := openMemPool(t, ctx, false, false)
	before := memSlot(t, key)
	beforeRefs := before.refs
	mainURI := before.mainURI[false]
	if _, err := mainOnly.DB().ExecContext(ctx, `CREATE TABLE IF NOT EXISTS mem_rb_main (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("main-only exec: %v", err)
	}
	if _, err := mainOnly.DB().ExecContext(ctx, `INSERT INTO mem_rb_main (id, v) VALUES (1, 'kept')`); err != nil {
		t.Fatalf("main-only insert: %v", err)
	}

	// A thumbs failure on a pre-existing referenced slot preserves the main
	// image, slot membership, references, and readiness. Wrap the live
	// slot's lease so ThumbsURI fails even though the factory is not called
	// for an existing slot; restore the production lease afterwards.
	memRegistryMu.Lock()
	live, ok := memMainSlots.Load(key)
	if !ok {
		memRegistryMu.Unlock()
		t.Fatalf("slot %q missing before referenced failure", key)
	}
	liveSlot := live.(*memDBSlot)
	prodLease := liveSlot.lease
	liveSlot.lease = &failingThumbsLease{memDBLease: prodLease}
	memRegistryMu.Unlock()
	_, failErr2 := openMemTestPoolWithConfig(ctx, memTestConfig(false, true))
	memRegistryMu.Lock()
	if cur, ok := memMainSlots.Load(key); ok {
		cur.(*memDBSlot).lease = prodLease
	}
	memRegistryMu.Unlock()
	if failErr2 == nil {
		t.Fatal("expected thumbs materialization failure on referenced slot")
	}
	after := memSlot(t, key)
	if after.mainURI[false] != mainURI {
		t.Fatalf("main image changed after failed thumbs acquire: %q vs %q", after.mainURI[false], mainURI)
	}
	if !after.mainReady || after.thumbsReady {
		t.Fatalf("slot readiness damaged: mainReady=%v thumbsReady=%v", after.mainReady, after.thumbsReady)
	}
	if after.refs != beforeRefs {
		t.Fatalf("refs = %d, want %d", after.refs, beforeRefs)
	}

	var kept string
	if err := mainOnly.DB().QueryRowContext(ctx, `SELECT v FROM mem_rb_main WHERE id = 1`).Scan(&kept); err != nil {
		t.Fatalf("existing pool query after failure: %v", err)
	}
	if kept != "kept" {
		t.Fatalf("existing pool row = %q, want kept", kept)
	}
	if err := mainOnly.Close(); err != nil {
		t.Fatalf("main-only close: %v", err)
	}
	if _, ok := memMainSlots.Load(key); ok {
		t.Fatalf("slot %q present after final close", key)
	}
}

func TestMemDBConcurrentFinalReleaseAndAcquire(t *testing.T) {
	ctx := context.Background()
	key := currentSchemaKey(t)

	first := openMemPool(t, ctx, false, true)
	// Release the first pool's reference before the race so the racy pair
	// is exactly one final close against one concurrent open.
	if err := first.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	seed := openMemPool(t, ctx, false, true)
	c, err := seed.Get()
	if err != nil {
		t.Fatalf("seed get: %v", err)
	}
	mustExec(t, ctx, c.Conn, `CREATE TABLE IF NOT EXISTS mem_race_main (id INTEGER PRIMARY KEY, v TEXT)`)
	mustExec(t, ctx, c.Conn, `CREATE TABLE thumbs.mem_race_thumbs (id INTEGER PRIMARY KEY, v TEXT)`)
	seed.Put(c)

	var wg sync.WaitGroup
	wg.Add(2)
	var closeErr error
	var second *DbSQLConnPool
	var openErr error
	go func() {
		defer wg.Done()
		closeErr = seed.Close()
	}()
	go func() {
		defer wg.Done()
		second, openErr = NewDbSQLConnPool(ctx, "", memTestConfig(false, true))
	}()
	wg.Wait()
	if closeErr != nil {
		t.Fatalf("concurrent close: %v", closeErr)
	}
	if openErr != nil {
		t.Fatalf("concurrent open: %v", openErr)
	}
	defer second.Close()

	count := 0
	memMainSlots.Range(func(_, _ any) bool { count++; return true })
	v, ok := memMainSlots.Load(key)
	if !ok || count != 1 {
		t.Fatalf("registry slots = %d (present=%v), want exactly 1", count, ok)
	}
	if got := v.(*memDBSlot).refs; got != 1 && got != 2 {
		t.Fatalf("slot refs = %d, want 1 or 2 (racy second open may share the slot)", got)
	}

	third := openMemPool(t, ctx, false, true)
	defer third.Close()
	w, err := second.Get()
	if err != nil {
		t.Fatalf("second get: %v", err)
	}
	r, err := third.Get()
	if err != nil {
		second.Put(w)
		t.Fatalf("third get: %v", err)
	}
	mustExec(t, ctx, w.Conn, `INSERT INTO mem_race_main (id, v) VALUES (1, 'x')`)
	mustExec(t, ctx, w.Conn, `INSERT INTO thumbs.mem_race_thumbs (id, v) VALUES (1, 'y')`)
	if got := mustQueryText(t, ctx, r.Conn, `SELECT v FROM mem_race_main WHERE id = 1`); got != "x" {
		third.Put(r)
		second.Put(w)
		t.Fatalf("cross-pool main = %q, want x", got)
	}
	if got := mustQueryText(t, ctx, r.Conn, `SELECT v FROM thumbs.mem_race_thumbs WHERE id = 1`); got != "y" {
		third.Put(r)
		second.Put(w)
		t.Fatalf("cross-pool thumbs = %q, want y", got)
	}
	third.Put(r)
	second.Put(w)
}

func TestMemDBFlagWithFileDSNUsesFilePool(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "filemode.db")
	thumbsPath := filepath.Join(dir, "thumbs.db")
	if err := CopyMainTestDatabaseTo(mainPath); err != nil {
		t.Fatalf("copy main test database: %v", err)
	}
	if err := CopyThumbsTestDatabaseTo(thumbsPath); err != nil {
		t.Fatalf("copy thumbs test database: %v", err)
	}
	cfg := Config{
		DriverName:         "sqlite3",
		MaxConnections:     2,
		MinIdleConnections: 0,
		QueriesFunc:        gallerydb.NewCustomQueries,
		ThumbsDBPath:       thumbsPath,
		TestUseMemDB:       true,
	}
	dsn := "file:" + filepath.ToSlash(mainPath) + "?_txlock=immediate&mode=rwc"
	pool, err := NewDbSQLConnPool(ctx, dsn, cfg)
	if err != nil {
		t.Fatalf("file-backed pool with flag set: %v", err)
	}
	defer pool.Close()
	if pool.isMemory {
		t.Fatal("file DSN pool marked as memory-VFS mode")
	}
	cpc, err := pool.Get()
	if err != nil {
		t.Fatalf("file pool get: %v", err)
	}
	defer pool.Put(cpc)
	if _, err := cpc.Conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS filemode (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("file pool exec: %v", err)
	}
}

func TestMemDBLazyThumbsMaterialization(t *testing.T) {
	ctx := context.Background()
	latestKey, err := migrations.LatestSchemaKey()
	if err != nil {
		t.Fatalf("LatestSchemaKey: %v", err)
	}
	if isolatedEnsureSchemaKey == latestKey {
		t.Fatalf("isolated schema key %q matches live latest schema key", isolatedEnsureSchemaKey)
	}
	prior := memDBSchemaKey
	memDBSchemaKey = func() (string, error) { return isolatedEnsureSchemaKey, nil }
	t.Cleanup(func() {
		memDBSchemaKey = prior
		memMainSlots.Delete(isolatedEnsureSchemaKey)
	})
	if _, ok := memMainSlots.Load(isolatedEnsureSchemaKey); ok {
		t.Fatalf("slot %q present before test", isolatedEnsureSchemaKey)
	}

	pool, err := NewDbSQLConnPool(ctx, "", memTestConfig(false, false))
	if err != nil {
		t.Fatalf("NewDbSQLConnPool main-only: %v", err)
	}
	defer func() {
		if cerr := pool.Close(); cerr != nil {
			t.Errorf("close: %v", cerr)
		}
	}()
	s := memSlot(t, isolatedEnsureSchemaKey)
	if !s.mainReady || s.thumbsReady {
		t.Fatalf("main-only readiness: mainReady=%v thumbsReady=%v", s.mainReady, s.thumbsReady)
	}
	if uri, ok := s.thumbsURI[false]; ok && uri != "" {
		t.Fatalf("main-only thumbs URI = %q, want empty", uri)
	}
	if uri, ok := s.thumbsURI[true]; ok && uri != "" {
		t.Fatalf("main-only thumbs URI = %q, want empty", uri)
	}

	// CustomQueries prepares thumbs.thumbnail_blobs statements per connection,
	// so a pool without a thumbs source cannot serve Get(). Drive the
	// main-only pool through its raw *sql.DB handle instead.
	conn, err := pool.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("get raw conn: %v", err)
	}
	defer conn.Close()
	assertMemTablePresent(t, ctx, conn, "main", "config")
}
