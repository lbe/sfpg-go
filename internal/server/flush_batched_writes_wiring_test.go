//go:build integration

package server

import (
	"context"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/server/files"
)

// createWiringTestImage writes a 1x1 JPEG into dir/name and returns the filename
// (relative, matching files.FileProcessor.ProcessFile expectations).
func createWiringTestImage(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("create test image: %v", err)
	}
	if err := jpeg.Encode(f, img, nil); err != nil {
		f.Close()
		t.Fatalf("encode test image: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close test image: %v", err)
	}
	return name
}

// waitForPendingZero polls the writebatcher until PendingCount reaches 0 or
// times out. The writebatcher flushes asynchronously on its 50ms timer.
func waitForPendingZero(t *testing.T, wb interface{ PendingCount() int64 }, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if wb.PendingCount() == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("writebatcher did not drain within %v; pending=%d", timeout, wb.PendingCount())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestFlushBatchedWrites_Wiring_ThreadsPreparedQueries verifies the writebatcher
// write-path wiring: BeginTx stashes the pooled connection's prepared
// *CustomQueries on app.batcherQueries, and flushBatchedWrites threads it via
// WithTx(tx) into WriteFileInTx. This is the integration that was broken before
// the refactor (WriteFileInTx rebuilt an unprepared NewCustomQueries(tx),
// discarding the prepared statements and recompiling every SQL statement).
//
// Subtest "end_to_end" drives the REAL app.writeBatcher: Submit a processed
// file, wait for the flush, and assert the file + thumbnail landed in the DB.
// This exercises BeginTx -> flushBatchedWrites -> WriteFileInTx -> Commit with
// the real prepared-queries wiring.
//
// Subtest "nil_batcherQueries_panics" is the regression guard: under the old
// (broken) wiring, flushBatchedWrites did not touch app.batcherQueries at all,
// so a nil value was harmless. Under the corrected wiring, flushBatchedWrites
// dereferences app.batcherQueries.WithTx(tx), so a nil value must panic. If
// this subtest ever stops panicking, the wiring has regressed.
func TestFlushBatchedWrites_Wiring_ThreadsPreparedQueries(t *testing.T) {
	t.Run("end_to_end", func(t *testing.T) {
		app := CreateApp(t)
		t.Cleanup(func() {
			if app.writeBatcher != nil {
				_ = app.writeBatcher.Close()
			}
		})
		ctx := context.Background()

		// Process a real image into a *files.File (with thumbnail).
		imgName := createWiringTestImage(t, app.imagesDir, "wiring_test.jpg")
		file, err := app.fileProcessor.ProcessFile(ctx, imgName)
		if err != nil {
			t.Fatalf("ProcessFile: %v", err)
		}
		if file.Thumbnail == nil {
			t.Fatal("ProcessFile did not generate a thumbnail")
		}

		// Submit through the REAL writebatcher, which exercises the real
		// BeginTx/Flush/OnSuccess wiring (the site of the original bug).
		if err := app.writeBatcher.Submit(BatchedWrite{File: file}); err != nil {
			t.Fatalf("writeBatcher.Submit: %v", err)
		}
		waitForPendingZero(t, app.writeBatcher, 5*time.Second)

		// Verify the file and thumbnail landed in the DB.
		cpcRo, err := app.dbRoPool.Get()
		if err != nil {
			t.Fatalf("dbRoPool.Get: %v", err)
		}
		defer app.dbRoPool.Put(cpcRo)

		storedPath := imgName // ProcessFile stores the path argument verbatim as file.Path
		dbFile, err := cpcRo.Queries.GetFileByPath(ctx, storedPath)
		if err != nil {
			t.Fatalf("GetFileByPath: %v", err)
		}
		if dbFile.ID == 0 {
			t.Error("file ID is 0 after writebatcher flush")
		}
		thumbExists, err := cpcRo.Queries.GetThumbnailExistsViewByID(ctx, dbFile.ID)
		if err != nil {
			t.Fatalf("GetThumbnailExistsViewByID: %v", err)
		}
		if !thumbExists {
			t.Error("thumbnail does not exist after writebatcher flush")
		}
	})

	t.Run("nil_batcherQueries_panics", func(t *testing.T) {
		app := CreateApp(t)
		t.Cleanup(func() {
			if app.writeBatcher != nil {
				_ = app.writeBatcher.Close()
			}
		})
		ctx := context.Background()

		// Sabotage the wiring: app.batcherQueries is what flushBatchedWrites
		// must now dereference. Force it nil to prove the dependency exists.
		app.batcherQueries = nil

		cpcRw, err := app.dbRwPool.Get()
		if err != nil {
			t.Fatalf("dbRwPool.Get: %v", err)
		}
		defer app.dbRwPool.Put(cpcRw)
		tx, err := cpcRw.Conn.BeginTx(ctx, nil)
		if err != nil {
			t.Fatalf("BeginTx: %v", err)
		}
		defer tx.Rollback()

		// A single-file batch is enough; the panic fires at
		// app.batcherQueries.WithTx(tx) before any file is processed.
		batch := []BatchedWrite{{File: &files.File{Path: "nil_wiring_probe.jpg"}}}

		panicked := false
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = true
				}
			}()
			_ = app.flushBatchedWrites(ctx, tx, batch)
		}()
		if !panicked {
			t.Fatal("flushBatchedWrites did not panic with nil app.batcherQueries; " +
				"the prepared-queries wiring has regressed (flushBatchedWrites no longer " +
				"threads app.batcherQueries into WriteFileInTx)")
		}
	})
}
