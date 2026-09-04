package modulestate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/metrics"
	"github.com/lbe/sfpg-go/migrations"
)

func setupModuleStatePool(t *testing.T) (*dbconnpool.DbSQLConnPool, func()) {
	t.Helper()

	tempDir := t.TempDir()
	dbfile := filepath.Join(tempDir, "module_state.db")
	thumbsDBPath := filepath.Join(tempDir, "thumbs.db")
	mmapSize := strconv.Itoa(39 * 1024 * 1024 * 1024)
	params := []string{
		"_cache_size=10240",
		"_pragma=cache(shared)",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=busy_timeout(5000)",
		"_pragma=temp_store(memory)",
		"_pragma=foreign_keys(true)",
		"_pragma=mmap_size(" + mmapSize + ")",
		"_txlock=deferred",
	}
	dsn := filepath.ToSlash(dbfile) + "?" + strings.Join(params, "&")

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("failed to open test database: %v", err)
	}

	driver, err := sqlite.WithInstance(db, &sqlite.Config{})
	if err != nil {
		db.Close()
		t.Fatalf("failed to create sqlite driver instance: %v", err)
	}

	d, err := iofs.New(migrations.FS, "migrations")
	if err != nil {
		db.Close()
		t.Fatalf("failed to create iofs source driver: %v", err)
	}

	m, err := migrate.NewWithInstance("iofs", d, "sqlite", driver)
	if err != nil {
		db.Close()
		t.Fatalf("failed to create migrate instance: %v", err)
	}

	if upErr := m.Up(); upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		db.Close()
		t.Fatalf("failed to apply migrations: %v", upErr)
	}

	thumbsMigrator, err := migrations.NewThumbsMigrator(thumbsDBPath)
	if err != nil {
		db.Close()
		t.Fatalf("failed to create thumbs migrator: %v", err)
	}
	if thumbsErr := thumbsMigrator.Up(); thumbsErr != nil && !errors.Is(thumbsErr, migrate.ErrNoChange) {
		thumbsMigrator.Close()
		db.Close()
		t.Fatalf("failed to apply thumbs migrations: %v", thumbsErr)
	}
	thumbsMigrator.Close()

	ctx := context.Background()
	pool, err := dbconnpool.NewDbSQLConnPool(ctx, dsn, dbconnpool.Config{
		DriverName:         "sqlite3",
		ReadOnly:           false,
		MaxConnections:     2,
		MinIdleConnections: 1,
		QueriesFunc:        gallerydb.NewCustomQueries,
		ThumbsDBPath:       thumbsDBPath,
	})
	if err != nil {
		db.Close()
		t.Fatalf("failed to create db pool: %v", err)
	}

	cleanup := func() {
		_ = pool.Close()
		_ = db.Close()
	}

	return pool, cleanup
}

func TestService_IsActive_DefaultFalse(t *testing.T) {
	pool, cleanup := setupModuleStatePool(t)
	defer cleanup()

	svc := NewService(pool)
	active, err := svc.IsActive(context.Background(), "discovery")
	if err != nil {
		t.Fatalf("IsActive error: %v", err)
	}
	if active {
		t.Fatal("expected inactive when no row exists")
	}
}

func TestService_SetActive_Toggle(t *testing.T) {
	pool, cleanup := setupModuleStatePool(t)
	defer cleanup()

	svc := NewService(pool)
	ctx := context.Background()

	if err := svc.SetActive(ctx, "discovery", true); err != nil {
		t.Fatalf("SetActive(true) error: %v", err)
	}
	active, err := svc.IsActive(ctx, "discovery")
	if err != nil {
		t.Fatalf("IsActive error: %v", err)
	}
	if !active {
		t.Fatal("expected active after SetActive(true)")
	}

	err = svc.SetActive(ctx, "discovery", false)
	if err != nil {
		t.Fatalf("SetActive(false) error: %v", err)
	}
	active, err = svc.IsActive(ctx, "discovery")
	if err != nil {
		t.Fatalf("IsActive error: %v", err)
	}
	if active {
		t.Fatal("expected inactive after SetActive(false)")
	}
}

func TestService_GetLastStartedAt_AfterSetActiveTrue(t *testing.T) {
	pool, cleanup := setupModuleStatePool(t)
	defer cleanup()

	svc := NewService(pool)
	ctx := context.Background()

	if err := svc.SetActive(ctx, "discovery", true); err != nil {
		t.Fatalf("SetActive(true) error: %v", err)
	}

	lastStarted, ok, err := svc.GetLastStartedAt(ctx, "discovery")
	if err != nil {
		t.Fatalf("GetLastStartedAt error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true after SetActive(true)")
	}
	if lastStarted <= 0 {
		t.Fatalf("expected non-zero lastStarted, got %d", lastStarted)
	}
}

func TestService_GetLastStartedAt_AfterSetActiveFalse(t *testing.T) {
	pool, cleanup := setupModuleStatePool(t)
	defer cleanup()

	svc := NewService(pool)
	ctx := context.Background()

	if err := svc.SetActive(ctx, "discovery", true); err != nil {
		t.Fatalf("SetActive(true) error: %v", err)
	}
	afterTrue, _, err := svc.GetLastStartedAt(ctx, "discovery")
	if err != nil {
		t.Fatalf("GetLastStartedAt after true: %v", err)
	}

	if setErr := svc.SetActive(ctx, "discovery", false); setErr != nil {
		t.Fatalf("SetActive(false) error: %v", setErr)
	}
	afterFalse, ok, err := svc.GetLastStartedAt(ctx, "discovery")
	if err != nil {
		t.Fatalf("GetLastStartedAt after false: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true after SetActive(false), last_started_at is unchanged")
	}
	if afterFalse != afterTrue {
		t.Fatalf("lastStarted should be unchanged: got %d, want %d", afterFalse, afterTrue)
	}
}

func TestService_GetLastStartedAt_NoRow(t *testing.T) {
	pool, cleanup := setupModuleStatePool(t)
	defer cleanup()

	svc := NewService(pool)
	ctx := context.Background()

	lastStarted, ok, err := svc.GetLastStartedAt(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetLastStartedAt error: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for module with no row")
	}
	if lastStarted != 0 {
		t.Fatalf("expected lastStarted=0 for no row, got %d", lastStarted)
	}
}

type fakePool struct {
	getFunc func() (*dbconnpool.CpConn, error)
	putFunc func(*dbconnpool.CpConn)
}

func (f *fakePool) Get() (*dbconnpool.CpConn, error) { return f.getFunc() }
func (f *fakePool) Put(cpc *dbconnpool.CpConn)       { f.putFunc(cpc) }

type fakeQuerier struct {
	getFunc        func(ctx context.Context, name string) (gallerydb.ModuleState, error)
	setFunc        func(ctx context.Context, arg gallerydb.SetModuleStateParams) error
	setPayloadFunc func(ctx context.Context, arg gallerydb.SetModuleStatePayloadParams) error
}

func (f *fakeQuerier) GetModuleState(ctx context.Context, name string) (gallerydb.ModuleState, error) {
	return f.getFunc(ctx, name)
}
func (f *fakeQuerier) SetModuleState(ctx context.Context, arg gallerydb.SetModuleStateParams) error {
	return f.setFunc(ctx, arg)
}
func (f *fakeQuerier) SetModuleStatePayload(ctx context.Context, arg gallerydb.SetModuleStatePayloadParams) error {
	return f.setPayloadFunc(ctx, arg)
}

func TestService_IsActive_NilService(t *testing.T) {
	active, err := (*Service)(nil).IsActive(context.Background(), "discovery")
	if !errors.Is(err, sql.ErrConnDone) {
		t.Fatalf("expected sql.ErrConnDone, got %v", err)
	}
	if active {
		t.Fatal("expected inactive for nil service")
	}
}

func TestService_IsActive_NilPool(t *testing.T) {
	active, err := (&Service{}).IsActive(context.Background(), "discovery")
	if !errors.Is(err, sql.ErrConnDone) {
		t.Fatalf("expected sql.ErrConnDone, got %v", err)
	}
	if active {
		t.Fatal("expected inactive for nil pool")
	}
}

func TestService_IsActive_PoolGetError(t *testing.T) {
	putCalls := 0
	svc := &Service{dbRwPool: &fakePool{
		getFunc: func() (*dbconnpool.CpConn, error) {
			return nil, errors.New("get denied")
		},
		putFunc: func(*dbconnpool.CpConn) {
			putCalls++
		},
	}}

	active, err := svc.IsActive(context.Background(), "discovery")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "get denied") {
		t.Fatalf("expected error to wrap 'get denied', got %v", err)
	}
	if active {
		t.Fatal("expected inactive on pool get error")
	}
	if putCalls != 0 {
		t.Fatalf("expected Put not to be called, got %d calls", putCalls)
	}
}

func TestService_IsActive_QueryError(t *testing.T) {
	putCalls := 0
	returnedConn := &dbconnpool.CpConn{}
	svc := &Service{dbRwPool: &fakePool{
		getFunc: func() (*dbconnpool.CpConn, error) {
			return returnedConn, nil
		},
		putFunc: func(cpc *dbconnpool.CpConn) {
			putCalls++
			if cpc != returnedConn {
				t.Fatalf("Put received unexpected connection")
			}
		},
	}}

	original := queriesFromCpConn
	queriesFromCpConn = func(*dbconnpool.CpConn) moduleStateQuerier {
		return &fakeQuerier{
			getFunc: func(context.Context, string) (gallerydb.ModuleState, error) {
				return gallerydb.ModuleState{}, errors.New("query denied")
			},
		}
	}
	t.Cleanup(func() { queriesFromCpConn = original })

	active, err := svc.IsActive(context.Background(), "discovery")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "query denied") {
		t.Fatalf("expected error to wrap 'query denied', got %v", err)
	}
	if active {
		t.Fatal("expected inactive on query error")
	}
	if putCalls != 1 {
		t.Fatalf("expected Put called once, got %d calls", putCalls)
	}
}

func TestService_IsActive_RowInactive(t *testing.T) {
	putCalls := 0
	returnedConn := &dbconnpool.CpConn{}
	svc := &Service{dbRwPool: &fakePool{
		getFunc: func() (*dbconnpool.CpConn, error) {
			return returnedConn, nil
		},
		putFunc: func(*dbconnpool.CpConn) { putCalls++ },
	}}

	original := queriesFromCpConn
	queriesFromCpConn = func(*dbconnpool.CpConn) moduleStateQuerier {
		return &fakeQuerier{
			getFunc: func(context.Context, string) (gallerydb.ModuleState, error) {
				return gallerydb.ModuleState{Name: "discovery", IsActive: 0}, nil
			},
		}
	}
	t.Cleanup(func() { queriesFromCpConn = original })

	active, err := svc.IsActive(context.Background(), "discovery")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if active {
		t.Fatal("expected inactive when row.IsActive == 0")
	}
	if putCalls != 1 {
		t.Fatalf("expected Put called once, got %d calls", putCalls)
	}
}

func TestService_GetLastStartedAt_NilService(t *testing.T) {
	lastStarted, ok, err := (*Service)(nil).GetLastStartedAt(context.Background(), "discovery")
	if !errors.Is(err, sql.ErrConnDone) {
		t.Fatalf("expected sql.ErrConnDone, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for nil service")
	}
	if lastStarted != 0 {
		t.Fatalf("expected lastStarted=0, got %d", lastStarted)
	}
}

func TestService_GetLastStartedAt_NilPool(t *testing.T) {
	lastStarted, ok, err := (&Service{}).GetLastStartedAt(context.Background(), "discovery")
	if !errors.Is(err, sql.ErrConnDone) {
		t.Fatalf("expected sql.ErrConnDone, got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for nil pool")
	}
	if lastStarted != 0 {
		t.Fatalf("expected lastStarted=0, got %d", lastStarted)
	}
}

func TestService_GetLastStartedAt_PoolGetError(t *testing.T) {
	svc := &Service{dbRwPool: &fakePool{
		getFunc: func() (*dbconnpool.CpConn, error) {
			return nil, errors.New("get denied")
		},
		putFunc: func(*dbconnpool.CpConn) {},
	}}

	lastStarted, ok, err := svc.GetLastStartedAt(context.Background(), "discovery")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "get denied") {
		t.Fatalf("expected error to wrap 'get denied', got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false on pool get error")
	}
	if lastStarted != 0 {
		t.Fatalf("expected lastStarted=0, got %d", lastStarted)
	}
}

func TestService_GetLastStartedAt_QueryError(t *testing.T) {
	putCalls := 0
	returnedConn := &dbconnpool.CpConn{}
	svc := &Service{dbRwPool: &fakePool{
		getFunc: func() (*dbconnpool.CpConn, error) {
			return returnedConn, nil
		},
		putFunc: func(cpc *dbconnpool.CpConn) {
			putCalls++
			if cpc != returnedConn {
				t.Fatalf("Put received unexpected connection")
			}
		},
	}}

	original := queriesFromCpConn
	queriesFromCpConn = func(*dbconnpool.CpConn) moduleStateQuerier {
		return &fakeQuerier{
			getFunc: func(context.Context, string) (gallerydb.ModuleState, error) {
				return gallerydb.ModuleState{}, errors.New("query denied")
			},
		}
	}
	t.Cleanup(func() { queriesFromCpConn = original })

	lastStarted, ok, err := svc.GetLastStartedAt(context.Background(), "discovery")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "query denied") {
		t.Fatalf("expected error to wrap 'query denied', got %v", err)
	}
	if ok {
		t.Fatal("expected ok=false on query error")
	}
	if lastStarted != 0 {
		t.Fatalf("expected lastStarted=0, got %d", lastStarted)
	}
	if putCalls != 1 {
		t.Fatalf("expected Put called once, got %d calls", putCalls)
	}
}

func TestService_GetLastStartedAt_NullStartedAt(t *testing.T) {
	putCalls := 0
	returnedConn := &dbconnpool.CpConn{}
	svc := &Service{dbRwPool: &fakePool{
		getFunc: func() (*dbconnpool.CpConn, error) {
			return returnedConn, nil
		},
		putFunc: func(*dbconnpool.CpConn) { putCalls++ },
	}}

	original := queriesFromCpConn
	queriesFromCpConn = func(*dbconnpool.CpConn) moduleStateQuerier {
		return &fakeQuerier{
			getFunc: func(context.Context, string) (gallerydb.ModuleState, error) {
				return gallerydb.ModuleState{Name: "discovery", LastStartedAt: sql.NullInt64{Valid: false}}, nil
			},
		}
	}
	t.Cleanup(func() { queriesFromCpConn = original })

	lastStarted, ok, err := svc.GetLastStartedAt(context.Background(), "discovery")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for null LastStartedAt")
	}
	if lastStarted != 0 {
		t.Fatalf("expected lastStarted=0, got %d", lastStarted)
	}
	if putCalls != 1 {
		t.Fatalf("expected Put called once, got %d calls", putCalls)
	}
}

func TestService_SetActive_QueryError(t *testing.T) {
	putCalls := 0
	returnedConn := &dbconnpool.CpConn{}
	svc := &Service{dbRwPool: &fakePool{
		getFunc: func() (*dbconnpool.CpConn, error) {
			return returnedConn, nil
		},
		putFunc: func(cpc *dbconnpool.CpConn) {
			putCalls++
			if cpc != returnedConn {
				t.Fatalf("Put received unexpected connection")
			}
		},
	}}

	original := queriesFromCpConn
	var receivedArg gallerydb.SetModuleStateParams
	queriesFromCpConn = func(*dbconnpool.CpConn) moduleStateQuerier {
		return &fakeQuerier{
			setFunc: func(_ context.Context, arg gallerydb.SetModuleStateParams) error {
				receivedArg = arg
				return errors.New("set denied")
			},
		}
	}
	t.Cleanup(func() { queriesFromCpConn = original })

	err := svc.SetActive(context.Background(), "discovery", true)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "set denied") {
		t.Fatalf("expected error to wrap 'set denied', got %v", err)
	}
	if receivedArg.IsActive != 1 {
		t.Fatalf("expected IsActive=1, got %d", receivedArg.IsActive)
	}
	if !receivedArg.LastStartedAt.Valid {
		t.Fatal("expected LastStartedAt to be valid")
	}
	if receivedArg.LastStartedAt.Int64 == 0 {
		t.Fatal("expected non-zero LastStartedAt")
	}
	if putCalls != 1 {
		t.Fatalf("expected Put called once, got %d calls", putCalls)
	}
}

// seedModuleStatePayload creates an inactive row for name and sets its payload.
func seedModuleStatePayload(t *testing.T, pool *dbconnpool.DbSQLConnPool, name, payload string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pool.Get()
	if err != nil {
		t.Fatalf("pool.Get error: %v", err)
	}
	defer pool.Put(conn)
	if setErr := conn.Queries.SetModuleState(ctx, gallerydb.SetModuleStateParams{Name: name, IsActive: 0}); setErr != nil {
		t.Fatalf("SetModuleState error: %v", setErr)
	}
	err = conn.Queries.SetModuleStatePayload(ctx, gallerydb.SetModuleStatePayloadParams{
		Payload: sql.NullString{String: payload, Valid: true},
		Name:    name,
	})
	if err != nil {
		t.Fatalf("SetModuleStatePayload error: %v", err)
	}
}

// readRawModuleStatePayload returns the raw payload TEXT for name, failing the
// test when the row is missing or the payload is NULL.
func readRawModuleStatePayload(t *testing.T, pool *dbconnpool.DbSQLConnPool, name string) string {
	t.Helper()
	ctx := context.Background()
	conn, err := pool.Get()
	if err != nil {
		t.Fatalf("pool.Get error: %v", err)
	}
	defer pool.Put(conn)
	row, err := conn.Queries.GetModuleState(ctx, name)
	if err != nil {
		t.Fatalf("GetModuleState error: %v", err)
	}
	if !row.Payload.Valid {
		t.Fatal("expected non-null payload")
	}
	return row.Payload.String
}

// fileProcessingKeys returns the sorted top-level JSON keys inside a
// file_processing payload object.
func fileProcessingKeys(t *testing.T, payload string) []string {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		t.Fatalf("payload failed to parse as JSON object: %v", err)
	}
	fpRaw, ok := doc["file_processing"]
	if !ok {
		t.Fatal(`payload missing "file_processing" key`)
	}
	var fpObj map[string]json.RawMessage
	if err := json.Unmarshal(fpRaw, &fpObj); err != nil {
		t.Fatalf(`"file_processing" is not a JSON object: %v`, err)
	}
	keys := make([]string, 0, len(fpObj))
	for k := range fpObj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func TestService_SaveLoadFileProcessing_RoundTrip(t *testing.T) {
	pool, cleanup := setupModuleStatePool(t)
	defer cleanup()

	svc := NewService(pool)
	ctx := context.Background()
	name := "discovery"

	fp := metrics.FileProcessingMetrics{
		TotalFound:      15666608,
		AlreadyExisting: 15620677,
		NewlyInserted:   40000,
		SkippedInvalid:  5931,
		InFlight:        42, // live state; must never be persisted
	}
	if err := svc.SaveFileProcessing(ctx, name, fp); err != nil {
		t.Fatalf("SaveFileProcessing error: %v", err)
	}

	got, err := svc.LoadFileProcessing(ctx, name)
	if err != nil {
		t.Fatalf("LoadFileProcessing error: %v", err)
	}
	fp.InFlight = 0
	if got != fp {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, fp)
	}

	wantKeys := []string{"already_existing", "newly_inserted", "skipped_invalid", "total_found"}
	gotKeys := fileProcessingKeys(t, readRawModuleStatePayload(t, pool, name))
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf(`file_processing keys = %v, want exactly %v (InFlight must be omitted)`, gotKeys, wantKeys)
	}
}

func TestService_SaveFileProcessing_MergesExistingKeys(t *testing.T) {
	pool, cleanup := setupModuleStatePool(t)
	defer cleanup()

	svc := NewService(pool)
	ctx := context.Background()
	name := "discovery"

	seedModuleStatePayload(t, pool, name, `{"other":true}`)

	if err := svc.SaveFileProcessing(ctx, name, metrics.FileProcessingMetrics{NewlyInserted: 7}); err != nil {
		t.Fatalf("SaveFileProcessing error: %v", err)
	}

	payload := readRawModuleStatePayload(t, pool, name)
	if !strings.Contains(payload, `"other":true`) {
		t.Fatalf("sibling key lost after merge: %s", payload)
	}

	got, err := svc.LoadFileProcessing(ctx, name)
	if err != nil {
		t.Fatalf("LoadFileProcessing error: %v", err)
	}
	if got.NewlyInserted != 7 {
		t.Fatalf("NewlyInserted lost after merge: got %d, want 7", got.NewlyInserted)
	}

	gotKeys := fileProcessingKeys(t, payload)
	if !slices.Equal(gotKeys, []string{"already_existing", "newly_inserted", "skipped_invalid", "total_found"}) {
		t.Fatalf(`file_processing keys = %v, want only the four locked keys`, gotKeys)
	}
}

func TestService_LoadFileProcessing_MissingRow(t *testing.T) {
	pool, cleanup := setupModuleStatePool(t)
	defer cleanup()

	svc := NewService(pool)
	got, err := svc.LoadFileProcessing(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("expected nil error for missing row, got: %v", err)
	}
	if got != (metrics.FileProcessingMetrics{}) {
		t.Fatalf("expected zero value for missing row, got %+v", got)
	}
}

func TestService_LoadFileProcessing_BadJSON(t *testing.T) {
	pool, cleanup := setupModuleStatePool(t)
	defer cleanup()

	svc := NewService(pool)
	seedModuleStatePayload(t, pool, "discovery", "not-json")

	got, err := svc.LoadFileProcessing(context.Background(), "discovery")
	if err == nil {
		t.Fatal("expected error for corrupt payload so hydrate can log it")
	}
	if got != (metrics.FileProcessingMetrics{}) {
		t.Fatalf("expected zero value on bad JSON, got %+v", got)
	}
}
