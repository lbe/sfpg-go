//go:build integration

package gallerydb

import (
	"testing"
)

// TestPreparedStatementsRoutingInvariant guards against the regression where
// the writebatcher write path constructed queries via NewCustomQueries (which
// does NOT prepare statements) instead of threading the pooled connection's
// prepared *CustomQueries through WithTx. With nil statement fields, every
// query falls through to the raw-SQL branch (tx.QueryRowContext) and recompiles
// the SQL text on every call — silently destroying throughput.
//
// This test pins the invariant the write path relies on:
//   - NewCustomQueries yields nil prepared statements (raw-SQL routing).
//   - PrepareCustomQueries + WithTx yields non-nil statements AND sets tx so
//     that Queries.queryRow/exec route through tx.StmtContext(preparedStmt).
//
// If either property changes, the write path's performance characteristics
// change with it, and this test is the canary.
func TestPreparedStatementsRoutingInvariant(t *testing.T) {
	db, prepared, ctx := setupTestDB(t)

	// --- NewCustomQueries: the BROKEN path (must have nil stmts) ---
	unprepared := NewCustomQueries(db)
	if unprepared.Queries.getFolderByPathStmt != nil {
		t.Error("NewCustomQueries should leave sqlc statements nil (raw-SQL routing); getFolderByPathStmt is non-nil")
	}
	if unprepared.Queries.upsertFileReturningFileStmt != nil {
		t.Error("NewCustomQueries should leave sqlc statements nil; upsertFileReturningFileStmt is non-nil")
	}
	if unprepared.upsertThumbnailBlobStmt != nil {
		t.Error("NewCustomQueries should leave custom statements nil; upsertThumbnailBlobStmt is non-nil")
	}
	if unprepared.queryFilesForFolderIndexRebuildStmt != nil {
		t.Error("NewCustomQueries should leave custom statements nil; queryFilesForFolderIndexRebuildStmt is non-nil")
	}
	if unprepared.countFilesForFolderIndexRebuildStmt != nil {
		t.Error("NewCustomQueries should leave custom statements nil; countFilesForFolderIndexRebuildStmt is non-nil")
	}
	if unprepared.Queries.tx != nil {
		t.Error("NewCustomQueries should not bind a transaction; tx is non-nil")
	}

	// --- PrepareCustomQueries + WithTx: the CORRECT path (must be prepared + tx-bound) ---
	// Begin a tx on the same connection so WithTx can bind it.
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()

	qtx := prepared.WithTx(tx)
	if qtx.Queries.getFolderByPathStmt == nil {
		t.Error("WithTx must propagate prepared sqlc statements; getFolderByPathStmt is nil")
	}
	if qtx.Queries.upsertFileReturningFileStmt == nil {
		t.Error("WithTx must propagate prepared sqlc statements; upsertFileReturningFileStmt is nil")
	}
	if qtx.upsertThumbnailBlobStmt == nil {
		t.Error("WithTx must propagate prepared custom statements; upsertThumbnailBlobStmt is nil")
	}
	if qtx.queryFilesForFolderIndexRebuildStmt == nil {
		t.Error("WithTx must propagate prepared custom statements; queryFilesForFolderIndexRebuildStmt is nil")
	}
	if qtx.countFilesForFolderIndexRebuildStmt == nil {
		t.Error("WithTx must propagate prepared custom statements; countFilesForFolderIndexRebuildStmt is nil")
	}
	if qtx.Queries.tx == nil {
		t.Error("WithTx must bind the transaction so routing uses tx.StmtContext; tx is nil")
	}
	if qtx.Queries.tx != tx {
		t.Error("WithTx bound the wrong transaction")
	}
}
