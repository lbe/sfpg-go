package writebatcher

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
)

// BenchmarkWriteBatcher_Submit measures raw channel send throughput.
// BeginTx returns nil, so items are reported to OnError (or slog) but
// not written to a database.  This isolates Submit overhead from flush cost.
func BenchmarkWriteBatcher_Submit(b *testing.B) {
	ctx := context.Background()
	cfg := Config[int]{
		BeginTx: func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
		Flush:   func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		OnError: func(err error, batch []int) {},
	}
	wb, err := New(ctx, cfg)
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	defer wb.Close()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if submitErr := wb.Submit(i); submitErr != nil {
			b.Fatalf("Submit: %v", submitErr)
		}
	}
}

func BenchmarkSubmitWithSizeFunc(b *testing.B) {
	ctx := context.Background()
	cfg := Config[int]{
		BeginTx:       func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
		Flush:         func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		OnError:       func(err error, batch []int) {},
		MaxBatchBytes: 1000,
		SizeFunc:      func(i int) int64 { return 10 },
	}
	wb, err := New(ctx, cfg)
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	defer wb.Close()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if submitErr := wb.Submit(i); submitErr != nil {
			b.Fatalf("Submit: %v", submitErr)
		}
	}
}

func BenchmarkWriteBatcher_Integration(b *testing.B) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		b.Fatal(err)
	}
	defer db.Close()

	if _, execErr := db.Exec("CREATE TABLE bench (id INTEGER)"); execErr != nil {
		b.Fatalf("db.Exec: %v", execErr)
	}

	ctx := context.Background()
	cfg := Config[int]{
		BeginTx: func(ctx context.Context) (*sql.Tx, error) { return db.BeginTx(ctx, nil) },
		Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error {
			stmt, err := tx.PrepareContext(ctx, "INSERT INTO bench (id) VALUES (?)")
			if err != nil {
				return err
			}
			defer stmt.Close()
			for _, item := range batch {
				if _, err := stmt.ExecContext(ctx, item); err != nil {
					return err
				}
			}
			return nil
		},
		MaxBatchSize: 100,
	}

	wb, newErr := New(ctx, cfg)
	if newErr != nil {
		b.Fatalf("New: %v", newErr)
	}
	defer wb.Close()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for errors.Is(wb.Submit(i), ErrFull) {
			// Busy-wait backoff for benchmark throughput measurement.
		}
	}
}
