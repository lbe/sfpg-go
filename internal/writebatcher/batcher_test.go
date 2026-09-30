package writebatcher

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
)

func TestNew(t *testing.T) {
	ctx := context.Background()

	t.Run("returns error when BeginTx is nil", func(t *testing.T) {
		cfg := Config[int]{
			Flush: func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		}
		_, err := New(ctx, cfg)
		if err == nil {
			t.Error("expected error when BeginTx is nil")
		}
	})

	t.Run("returns error when Flush is nil", func(t *testing.T) {
		cfg := Config[int]{
			BeginTx: func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
		}
		_, err := New(ctx, cfg)
		if err == nil {
			t.Error("expected error when Flush is nil")
		}
	})

	t.Run("returns error when MaxBatchBytes > 0 but SizeFunc is nil", func(t *testing.T) {
		cfg := Config[int]{
			BeginTx:       func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:         func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
			MaxBatchBytes: 1024,
			SizeFunc:      nil,
		}
		_, err := New(ctx, cfg)
		if err == nil {
			t.Error("expected error when MaxBatchBytes > 0 but SizeFunc is nil")
		}
	})

	t.Run("applies default values", func(t *testing.T) {
		cfg := Config[int]{
			BeginTx: func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:   func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		}
		wb, err := New(ctx, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer wb.Close()

		if wb.cfg.MaxBatchSize != 50 {
			t.Errorf("expected MaxBatchSize 50, got %d", wb.cfg.MaxBatchSize)
		}
		if wb.cfg.FlushInterval != 200*time.Millisecond {
			t.Errorf("expected FlushInterval 200ms, got %v", wb.cfg.FlushInterval)
		}
		if wb.cfg.ChannelSize != 1024 {
			t.Errorf("expected ChannelSize 1024, got %d", wb.cfg.ChannelSize)
		}
	})

	t.Run("can be closed immediately", func(t *testing.T) {
		cfg := Config[int]{
			BeginTx: func(ctx context.Context) (*sql.Tx, error) { return nil, nil },
			Flush:   func(ctx context.Context, tx *sql.Tx, batch []int) error { return nil },
		}
		wb, err := New(ctx, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := wb.Close(); err != nil {
			t.Errorf("Close() returned error: %v", err)
		}
	})
}
