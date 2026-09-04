// Package modulestate tracks active/inactive state for server subsystems.
package modulestate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/gallerydb"
	"github.com/lbe/sfpg-go/internal/server/metrics"
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
	SetModuleStatePayload(ctx context.Context, arg gallerydb.SetModuleStatePayloadParams) error
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

// fileProcessingPayloadKey is the module_state.payload key holding the last-run
// file processing counters.
const fileProcessingPayloadKey = "file_processing"

// fileProcessingPersist is the persisted subset of metrics.FileProcessingMetrics.
// InFlight is intentionally omitted: it is live state and must never be written
// to or hydrated from module_state.payload.
//
// json.Marshal of metrics.FileProcessingMetrics must NOT be used for persistence
// because its in_flight field would be written after a drain.
// Loading also unmarshals into this shape so a stale in_flight key in an old row
// is ignored rather than hydrated.
type fileProcessingPersist struct {
	TotalFound      uint64 `json:"total_found"`
	AlreadyExisting uint64 `json:"already_existing"`
	NewlyInserted   uint64 `json:"newly_inserted"`
	SkippedInvalid  uint64 `json:"skipped_invalid"`
}

// SaveFileProcessing persists the four last-run file processing counters under
// module_state.payload[file_processing] for name. Sibling JSON keys are merged
// and preserved; InFlight is never written. A missing row is created as inactive
// before the payload is stored.
func (s *Service) SaveFileProcessing(ctx context.Context, name string, fp metrics.FileProcessingMetrics) error {
	if s == nil || s.dbRwPool == nil {
		return sql.ErrConnDone
	}
	cpcRw, err := s.dbRwPool.Get()
	if err != nil {
		return err
	}
	defer s.dbRwPool.Put(cpcRw)

	q := queriesFromCpConn(cpcRw)

	doc := map[string]json.RawMessage{}
	row, err := q.GetModuleState(ctx, name)
	switch {
	case err == nil:
		if row.Payload.Valid && row.Payload.String != "" {
			// A corrupt existing payload is dropped rather than failing the save.
			if unmarshalErr := json.Unmarshal([]byte(row.Payload.String), &doc); unmarshalErr != nil {
				doc = map[string]json.RawMessage{}
			}
		}
	case errors.Is(err, sql.ErrNoRows):
		if setErr := q.SetModuleState(ctx, gallerydb.SetModuleStateParams{Name: name, IsActive: 0}); setErr != nil {
			return setErr
		}
	default:
		return err
	}

	persistJSON, err := json.Marshal(fileProcessingPersist{
		TotalFound:      fp.TotalFound,
		AlreadyExisting: fp.AlreadyExisting,
		NewlyInserted:   fp.NewlyInserted,
		SkippedInvalid:  fp.SkippedInvalid,
	})
	if err != nil {
		return err
	}
	doc[fileProcessingPayloadKey] = persistJSON

	merged, err := json.Marshal(doc)
	if err != nil {
		return err
	}

	return q.SetModuleStatePayload(ctx, gallerydb.SetModuleStatePayloadParams{
		Payload: sql.NullString{String: string(merged), Valid: true},
		Name:    name,
	})
}

// LoadFileProcessing returns the persisted last-run file processing counters for
// name. A missing row, a NULL/empty payload, and an absent file_processing key
// all yield the zero value with a nil error. A corrupt payload yields the zero
// value with a non-nil error so the caller can log it. InFlight is never
// hydrated.
func (s *Service) LoadFileProcessing(ctx context.Context, name string) (metrics.FileProcessingMetrics, error) {
	if s == nil || s.dbRwPool == nil {
		return metrics.FileProcessingMetrics{}, sql.ErrConnDone
	}
	cpcRw, err := s.dbRwPool.Get()
	if err != nil {
		return metrics.FileProcessingMetrics{}, err
	}
	defer s.dbRwPool.Put(cpcRw)

	row, err := queriesFromCpConn(cpcRw).GetModuleState(ctx, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return metrics.FileProcessingMetrics{}, nil
		}
		return metrics.FileProcessingMetrics{}, err
	}
	if !row.Payload.Valid || row.Payload.String == "" {
		return metrics.FileProcessingMetrics{}, nil
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(row.Payload.String), &doc); err != nil {
		return metrics.FileProcessingMetrics{}, err
	}
	fpRaw, ok := doc[fileProcessingPayloadKey]
	if !ok {
		return metrics.FileProcessingMetrics{}, nil
	}

	var persist fileProcessingPersist
	if err := json.Unmarshal(fpRaw, &persist); err != nil {
		return metrics.FileProcessingMetrics{}, err
	}
	return metrics.FileProcessingMetrics{
		TotalFound:      persist.TotalFound,
		AlreadyExisting: persist.AlreadyExisting,
		NewlyInserted:   persist.NewlyInserted,
		SkippedInvalid:  persist.SkippedInvalid,
	}, nil
}
