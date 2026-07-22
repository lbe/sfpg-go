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

	// rowsCloseFn is a testable hook for *sql.Rows.Close.
	rowsCloseFn = func(r *sql.Rows) error {
		return r.Close()
	}

	// rowsErrFn is a testable hook for *sql.Rows.Err.
	rowsErrFn = func(r *sql.Rows) error {
		return r.Err()
	}
)
