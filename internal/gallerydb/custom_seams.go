package gallerydb

import (
	"context"
	"database/sql"
)

// Package-level hooks for testing custom.sql.go only.
// sqlc-generated db.go and *.sql.go must never reference these variables.
var (
	// prepareContextFn is a testable hook for DBTX.PrepareContext.
	prepareContextFn = func(ctx context.Context, db DBTX, query string) (*sql.Stmt, error) {
		return db.PrepareContext(ctx, query)
	}

	// stmtCloseFn is a testable hook for *sql.Stmt.Close.
	stmtCloseFn = func(s *sql.Stmt) error {
		return s.Close()
	}

	// stmtExecContextFn is a testable hook for *sql.Stmt.ExecContext. The folder-index
	// INSERT flush calls it per row through the transaction-prepared statement so the
	// G6 test can count Exec calls (one Prepare, many Exec).
	stmtExecContextFn = func(ctx context.Context, s *sql.Stmt, args ...any) (sql.Result, error) {
		return s.ExecContext(ctx, args...)
	}

	// rowsCloseFn is a testable hook for *sql.Rows.Close.
	rowsCloseFn = func(r *sql.Rows) error {
		return r.Close()
	}

	// rowsErrFn is a testable hook for *sql.Rows.Err.
	rowsErrFn = func(r *sql.Rows) error {
		return r.Err()
	}
)
