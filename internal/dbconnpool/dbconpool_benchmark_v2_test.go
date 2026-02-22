package dbconnpool

import (
	"context"
	"fmt"
	mathrand "math/rand"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
	"go.local/sfpg/internal/gallerydb"
)

const (
	benchmarkMaxConnections = 2000
	benchmarkMinIdle        = 200
	numReaderPaths          = 1000
)

func verifyPragmas(b *testing.B, pool *DbSQLConnPool, poolName string) {
	b.Helper()
	pragmas := []string{
		"journal_mode",
		"synchronous",
		"busy_timeout",
		"cache_size",
		"temp_store",
		"foreign_keys",
		"mmap_size",
	}

	cpc, err := pool.Get()
	if err != nil {
		b.Fatalf(
			"Failed to get connection to verify pragmas for %s: %v",
			poolName, err,
		)
	}
	defer pool.Put(cpc)

	ctx := context.Background()
	b.Logf(
		"--- Verifying PRAGMAs for %s (%s) ---",
		poolName, pool.Config.DriverName,
	)
	for _, pragma := range pragmas {
		var value any
		err := cpc.Conn.QueryRowContext(
			ctx, "PRAGMA "+pragma,
		).Scan(&value)
		if err != nil {
			b.Logf("  PRAGMA %s: <Error: %v>", pragma, err)
		} else {
			b.Logf("  PRAGMA %s: %v", pragma, value)
		}
	}
}

func setupBenchmarkDB(b *testing.B, driverName string) (
	rwPool, roPool *DbSQLConnPool,
	readerPaths []string,
	cleanup func(),
) {
	b.Helper()

	dbPath, dbCleanup := setupTestDB(b)

	var rwDsn, roDsn string
	dsnParams := make(url.Values)

	switch driverName {
	case "sqlite":
		dsnParams.Add("_pragma", "cache(shared)")
		dsnParams.Add("_pragma", "journal_mode(WAL)")
		dsnParams.Add("_pragma", "synchronous(NORMAL)")
		dsnParams.Add("_pragma", "busy_timeout(5000)")
		dsnParams.Add("_pragma", "cache_size(10240)")
		dsnParams.Add("_pragma", "temp_store(memory)")
		dsnParams.Add("_pragma", "foreign_keys(false)")
		dsnParams.Add("_pragma", "mmap_size(41875931136)")

		dsnParams.Set("_txlock", "immediate")
		rwDsn = dbPath + "?" + dsnParams.Encode()

		dsnParams.Set("_txlock", "deferred")
		roDsn = dbPath + "?" + dsnParams.Encode()

	case "sqlite3":
		dsnParams["_pragma"] = []string{
			"journal_mode=WAL",
			"synchronous=NORMAL",
			"busy_timeout=5000",
			"cache_size=10240",
			"temp_store=memory",
			"foreign_keys=false",
			"mmap_size=" + strconv.Itoa(39*1024*1024*1024),
		}
		dsnParams.Add("cache", "shared")

		dsnParams.Set("_txlock", "immediate")
		rwDsn = dbPath + "?" + dsnParams.Encode()

		dsnParams.Set("_txlock", "deferred")
		roDsn = dbPath + "?" + dsnParams.Encode()

	default:
		b.Fatalf("Unsupported driver for benchmark: %s", driverName)
	}

	ctx := context.Background()

	rwConfig := Config{
		DriverName:         driverName,
		ReadOnly:           false,
		MaxConnections:     benchmarkMaxConnections,
		MinIdleConnections: benchmarkMinIdle,
		QueriesFunc:        gallerydb.NewCustomQueries,
	}

	roConfig := Config{
		DriverName:         driverName,
		ReadOnly:           true,
		MaxConnections:     benchmarkMaxConnections,
		MinIdleConnections: benchmarkMinIdle,
		QueriesFunc:        gallerydb.NewCustomQueries,
	}

	var err error
	rwPool, err = NewDbSQLConnPool(ctx, rwDsn, rwConfig)
	if err != nil {
		b.Fatalf("failed to create RW pool: %v", err)
	}

	roPool, err = NewDbSQLConnPool(ctx, roDsn, roConfig)
	if err != nil {
		b.Fatalf("failed to create RO pool: %v", err)
	}

	verifyPragmas(b, rwPool, "rwPool")
	verifyPragmas(b, roPool, "roPool")

	// Pre-populate file_paths for readers.
	readerPaths = make([]string, numReaderPaths)
	cpc, err := rwPool.Get()
	if err != nil {
		b.Fatalf("failed to get connection for pre-population: %v", err)
	}

	insertStmt, err := cpc.Conn.PrepareContext(ctx,
		"INSERT OR IGNORE INTO file_paths (path) VALUES (?)",
	)
	if err != nil {
		b.Fatalf("failed to prepare insert for pre-population: %v", err)
	}

	for i := range numReaderPaths {
		path := "/bench/reader/" + strconv.Itoa(i) + "/img.jpg"
		readerPaths[i] = path
		if _, err := insertStmt.ExecContext(ctx, path); err != nil {
			b.Fatalf("failed to pre-populate file_paths: %v", err)
		}
	}
	insertStmt.Close()
	rwPool.Put(cpc)

	cleanup = func() {
		rwPool.Close()
		roPool.Close()
		dbCleanup()
	}

	return rwPool, roPool, readerPaths, cleanup
}

func runReadWriteBenchmark(
	b *testing.B,
	driverName string,
	writers, readers int,
) {
	rwPool, roPool, readerPaths, cleanup :=
		setupBenchmarkDB(b, driverName)
	defer cleanup()

	writerCounter := int64(writers)
	var opCounter uint64
	ctx := context.Background()

	b.SetParallelism(writers + readers)
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		isWriter := atomic.AddInt64(&writerCounter, -1) >= 0

		if isWriter {
			cpc, err := rwPool.Get()
			if err != nil {
				b.Errorf("Writer: Get failed: %v", err)
				return
			}
			defer rwPool.Put(cpc)

			// Prepare once per writer goroutine on this connection.
			stmt, err := cpc.Conn.PrepareContext(ctx,
				"INSERT OR REPLACE INTO file_paths (path) VALUES (?)",
			)
			if err != nil {
				b.Errorf("Writer: Prepare failed: %v", err)
				return
			}
			defer stmt.Close()

			for pb.Next() {
				n := atomic.AddUint64(&opCounter, 1)
				path := "/bench/writer/" +
					strconv.FormatUint(n, 10) + "/img.jpg"
				if _, err := stmt.ExecContext(ctx, path); err != nil {
					b.Errorf("Writer: Exec failed: %v", err)
				}
			}
		} else {
			cpc, err := roPool.Get()
			if err != nil {
				b.Errorf("Reader: Get failed: %v", err)
				return
			}
			defer roPool.Put(cpc)

			// Prepare once per reader goroutine on this connection.
			stmt, err := cpc.Conn.PrepareContext(ctx,
				"SELECT id, path FROM file_paths WHERE path = ?",
			)
			if err != nil {
				b.Errorf("Reader: Prepare failed: %v", err)
				return
			}
			defer stmt.Close()

			var id int64
			var path string

			for pb.Next() {
				qPath := readerPaths[mathrand.Intn(numReaderPaths)]
				if err := stmt.QueryRowContext(
					ctx, qPath,
				).Scan(&id, &path); err != nil {
					b.Errorf("Reader: QueryRow failed: %v", err)
				}
			}
		}
	})

	b.StopTimer()
}

func BenchmarkModernc(b *testing.B) {
	driverName := "sqlite"
	scenarios := []struct {
		name    string
		writers int
		readers int
	}{
		{fmt.Sprintf("%dw_%dr", 1, 10), 1, 10},
		{fmt.Sprintf("%dw_%dr", 5, 50), 5, 50},
		{fmt.Sprintf("%dw_%dr", 10, 100), 10, 100},
	}

	for _, s := range scenarios {
		b.Run(s.name, func(b *testing.B) {
			runReadWriteBenchmark(b, driverName, s.writers, s.readers)
		})
	}
}
