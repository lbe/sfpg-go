// Package modulestate tracks active/inactive state for server subsystems.
package modulestate

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// pool is the subset of dbconnpool.DbSQLConnPool used by Service.
type pool interface {
	Get() (*dbconnpool.CpConn, error)
	Put(*dbconnpool.CpConn)
}

// moduleStateQuerier is the subset of gallerydb.CustomQueries used by Service.
type moduleStateQuerier interface {
	GetModuleState(ctx context.Context, name string) (gallerydb.ModuleState, error)
	SetModuleState(ctx context.Context, arg gallerydb.SetModuleStateParams) error
}

// queriesFromCpConn extracts the querier from a pool connection.
// Tests override this to return mock queriers without a real database.
var queriesFromCpConn = func(cpc *dbconnpool.CpConn) moduleStateQuerier {
	return cpc.Queries
}

// Service provides access to the module_state table.
type Service struct {
	dbRwPool pool
}

// NewService creates a new module state service.
func NewService(dbRwPool *dbconnpool.DbSQLConnPool) *Service {
	return &Service{dbRwPool: dbRwPool}
}

// SetActive sets the active state for a module and updates timestamps.
// When active=true, last_started_at is set and last_finished_at is left unchanged.
// When active=false, last_finished_at is set and last_started_at is left unchanged.
func (s *Service) SetActive(ctx context.Context, name string, active bool) error {
	if s == nil || s.dbRwPool == nil {
		return sql.ErrConnDone
	}
	cpcRw, err := s.dbRwPool.Get()
	if err != nil {
		return err
	}
	defer s.dbRwPool.Put(cpcRw)

	now := time.Now().Unix()
	var lastStarted sql.NullInt64
	var lastFinished sql.NullInt64
	if active {
		lastStarted = sql.NullInt64{Int64: now, Valid: true}
	} else {
		lastFinished = sql.NullInt64{Int64: now, Valid: true}
	}

	return queriesFromCpConn(cpcRw).SetModuleState(ctx, gallerydb.SetModuleStateParams{
		Name:           name,
		IsActive:       boolToInt(active),
		LastStartedAt:  lastStarted,
		LastFinishedAt: lastFinished,
	})
}

// IsActive returns true if the module is active.
// Missing rows are treated as inactive.
func (s *Service) IsActive(ctx context.Context, name string) (bool, error) {
	if s == nil || s.dbRwPool == nil {
		return false, sql.ErrConnDone
	}
	cpcRw, err := s.dbRwPool.Get()
	if err != nil {
		return false, err
	}
	defer s.dbRwPool.Put(cpcRw)

	row, err := queriesFromCpConn(cpcRw).GetModuleState(ctx, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	return row.IsActive != 0, nil
}

// GetLastStartedAt returns the last_started_at timestamp for a module.
// Returns (0, false, nil) if the module has no row or last_started_at is null.
func (s *Service) GetLastStartedAt(ctx context.Context, name string) (int64, bool, error) {
	if s == nil || s.dbRwPool == nil {
		return 0, false, sql.ErrConnDone
	}
	cpcRw, err := s.dbRwPool.Get()
	if err != nil {
		return 0, false, err
	}
	defer s.dbRwPool.Put(cpcRw)

	row, err := queriesFromCpConn(cpcRw).GetModuleState(ctx, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return row.LastStartedAt.Int64, row.LastStartedAt.Valid, nil
}

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
