// Package sqlite3stat provides optional SQLite connection status helpers for observability.
package sqlite3stat

import (
	"database/sql"
	"fmt"
	"log/slog"

	sqlite3 "github.com/ncruces/go-sqlite3"
	sqlite3driver "github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/humanize"
)

const (
	dbStatusCacheUsed  sqlite3.DBStatus = 1
	dbStatusSchemaUsed sqlite3.DBStatus = 2
	dbStatusStmtUsed   sqlite3.DBStatus = 3
)

// PutDebugHook is an optional hook for extra debug attributes on pool Put.
// A nil pointer or a nil *func is a no-op.
var PutDebugHook *func(*sql.Conn) []slog.Attr

// DefaultPutDebugAttrs returns db_status_mem when readable from conn.
func DefaultPutDebugAttrs(conn *sql.Conn) []slog.Attr {
	mem, ok := DBStatusMem(conn)
	if !ok {
		return nil
	}
	return []slog.Attr{slog.String("db_status_mem", humanize.Comma(mem).String())}
}

// PutDebugAttrs returns hook attrs when PutDebugHook is set; otherwise nil.
func PutDebugAttrs(conn *sql.Conn) []slog.Attr {
	if PutDebugHook == nil || *PutDebugHook == nil {
		return nil
	}
	return (*PutDebugHook)(conn)
}

// DBStatusMem returns the sum of CACHE_USED, SCHEMA_USED, and STMT_USED for conn.
func DBStatusMem(conn *sql.Conn) (int64, bool) {
	if conn == nil {
		return 0, false
	}

	var sum int64
	err := conn.Raw(func(driverConn any) error {
		dc, ok := driverConn.(sqlite3driver.Conn)
		if !ok {
			return fmt.Errorf("sqlite3stat: driver connection is not ncruces sqlite3")
		}
		raw := dc.Raw()
		for _, op := range []sqlite3.DBStatus{
			dbStatusCacheUsed,
			dbStatusSchemaUsed,
			dbStatusStmtUsed,
		} {
			cur, _, statusErr := raw.Status(op, false)
			if statusErr != nil {
				return statusErr
			}
			sum += cur
		}
		return nil
	})
	if err != nil {
		return 0, false
	}
	return sum, true
}
