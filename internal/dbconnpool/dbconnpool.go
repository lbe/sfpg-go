// Package dbconnpool provides a connection pool for SQLite databases using database/sql.
// It manages connection lifecycle including acquisition, validation, release and cleanup.
// The pool maintains both maximum total connections and minimum idle connections,
// automatically scaling between these bounds based on demand.
package dbconnpool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lbe/sfpg-go/internal/gallerydb"
)

// Common errors that can be returned by the connection pool.
var (
	// ErrPoolClosed is returned when attempting to get a connection from a closed pool.
	ErrPoolClosed = errors.New("connection pool is closed")
	// ErrConnectionUnavailable is returned when no connections are available and max connections reached.
	ErrConnectionUnavailable = errors.New("no connections available and max connections reached")
)

// ConnectionPool abstracts database connection pool operations.
// Implementations manage connection lifecycle, pooling, and health checks.
type ConnectionPool interface {
	// Get acquires a connection from the pool. Blocks if pool is at capacity.
	// Returns ErrPoolClosed if the pool has been closed.
	Get() (*CpConn, error)

	// Put returns a connection to the pool for reuse.
	// Safe to call with nil (no-op).
	Put(cpc *CpConn)

	// Close gracefully shuts down the pool, closing all connections.
	// Returns ErrPoolClosed if already closed.
	Close() error

	// DB returns the underlying *sql.DB for operations that need direct access
	// (e.g., migrations). Use sparingly.
	DB() *sql.DB

	// NumIdleConnections returns the current number of idle connections.
	NumIdleConnections() int

	// NumConnections returns the total number of connections (idle + in-use).
	NumConnections() int64
}

// Ensure DbSQLConnPool implements ConnectionPool
var _ ConnectionPool = (*DbSQLConnPool)(nil)

// ConnectionError represents an error that occurred with a specific connection.
type ConnectionError struct {
	Op  string // the operation that failed (e.g., "ping", "connect")
	Err error  // the underlying error
}

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("connection %s failed: %v", e.Op, e.Err)
}

func (e *ConnectionError) Unwrap() error {
	return e.Err
}

// Config holds the configuration parameters for DbSQLConnPool.
type Config struct {
	// DriverName specifies the name of the database driver to use (e.g., "sqlite").
	DriverName string

	// ReadOnly opens connections in read-only mode. When true, connection
	// creation is not serialized, allowing parallel preparation of statements.
	ReadOnly bool

	// Maximum number of connections the pool will create.
	MaxConnections int64

	// Minimum number of idle connections to maintain.
	MinIdleConnections int64

	// QueriesFunc is a function to instantiate a Queries object for a new connection.
	QueriesFunc func(db gallerydb.DBTX) *gallerydb.CustomQueries

	// MonitorInterval specifies how often the connection maintenance monitor should run.
	MonitorInterval time.Duration

	// HealthCheckThreshold is the minimum idle duration before a connection is
	// pinged on Get(). Connections idle for less than this are assumed healthy
	// and returned without a round-trip ping.
	// Default: 30s. Set to 0 to ping on every Get().
	HealthCheckThreshold time.Duration
}

// CpConn wraps an underlying *sql.Conn and holds a set of prepared queries
// associated with that connection.
type CpConn struct {
	// Conn is the underlying database connection.
	Conn *sql.Conn

	// Queries holds the sqlc-generated query methods for this connection.
	Queries *gallerydb.CustomQueries

	// idleSince tracks when this connection was last returned to the pool.
	// Used to determine whether a health-check ping is needed on Get().
	idleSince time.Time
}

func (cpc *CpConn) Close() error {
	var errs []error

	if qErr := cpc.Queries.Close(); qErr != nil {
		errs = append(errs, fmt.Errorf("queries close error: %w", qErr))
	}
	if cErr := cpc.Conn.Close(); cErr != nil {
		errs = append(errs, fmt.Errorf("connection close error: %w", cErr))
	}

	if len(errs) > 0 {
		for i, err := range errs {
			slog.Error("close error", "err", err, "index", i)
		}
		return errs[0]
	}
	return nil
}

func (cpc *CpConn) PragmaOptimize(ctx context.Context) {
	if _, err := cpc.Conn.ExecContext(ctx, `PRAGMA optimize;`); err != nil {
		slog.Debug("PRAGMA Optimize", "err", err)
	}
}

// DbSQLConnPool manages a pool of SQL database connections.
// It provides thread-safe access to connections through a buffered channel,
// maintains connection health, and automatically scales the pool size.
//
// All mutable state is managed via atomic operations and channels;
// no mutex is required on the Get/Put hot path.
type DbSQLConnPool struct {
	// Config holds the pool's configuration.
	Config Config

	// ctx manages connection lifecycles and cancellation.
	ctx context.Context

	// pool is the underlying database/sql connection pool.
	pool *sql.DB

	// connections is a buffered channel of available (idle) connections.
	connections chan *CpConn

	// maxConnections is the maximum number of connections in the pool.
	maxConnections int64

	// minIdleConnections is the minimum number of idle connections to maintain.
	minIdleConnections int64

	// monitorInterval specifies how often the monitor should run.
	monitorInterval time.Duration

	// healthCheckThreshold is the idle duration after which Get() pings
	// a connection before returning it. Connections idle for less than
	// this are returned immediately without a ping.
	healthCheckThreshold time.Duration

	// numConnections is the current total number of connections (idle + in-use).
	numConnections atomic.Int64

	// creationMu serializes connection creation for read-write pools to prevent
	// SQLITE_BUSY during concurrent statement preparation. Not used for read-only pools.
	creationMu sync.Mutex

	// done channel for graceful shutdown of Monitor.
	done chan struct{}

	// closed indicates if the pool has been closed (atomic for lock-free fast path).
	closed atomic.Bool
}

// newCpConn creates, pings, and initializes a new CpConn, wrapping a raw *sql.Conn.
// For read-write pools, creation is serialized via creationMu to avoid SQLITE_BUSY.
// For read-only pools, connections are created concurrently.
func (p *DbSQLConnPool) newCpConn() (*CpConn, error) {
	// Only serialize for read-write pools where concurrent statement
	// preparation can cause SQLITE_BUSY.
	if !p.Config.ReadOnly {
		p.creationMu.Lock()
		defer p.creationMu.Unlock()
	}

	conn, err := p.pool.Conn(p.ctx)
	if err != nil {
		return nil, &ConnectionError{Op: "connect", Err: err}
	}

	if err = conn.PingContext(p.ctx); err != nil {
		conn.Close()
		return nil, &ConnectionError{Op: "ping", Err: err}
	}

	queries, err := gallerydb.PrepareCustomQueries(p.ctx, conn)
	if err != nil {
		slog.Error("error preparing custom queries", "err", err)
		conn.Close()
		return nil, fmt.Errorf("error preparing custom queries: %w", err)
	}

	return &CpConn{
		Conn:    conn,
		Queries: queries,
	}, nil
}

// reserveSlot atomically increments numConnections if below maxConnections.
// Returns true if a slot was successfully reserved, false if at capacity.
func (p *DbSQLConnPool) reserveSlot() bool {
	for {
		current := p.numConnections.Load()
		if current >= p.maxConnections {
			return false
		}
		if p.numConnections.CompareAndSwap(current, current+1) {
			return true
		}
	}
}

// needsHealthCheck returns true if the connection has been idle long enough
// to warrant a ping before reuse.
func (p *DbSQLConnPool) needsHealthCheck(cpc *CpConn) bool {
	if p.healthCheckThreshold == 0 {
		return true
	}
	return !cpc.idleSince.IsZero() &&
		time.Since(cpc.idleSince) > p.healthCheckThreshold
}

// NewDbSQLConnPool creates a new connection pool with the specified parameters.
// driverName and dataSourceName are passed to sql.Open to create the base pool.
// config specifies the pool's connection limits and behavior.
// Returns error if the database connection cannot be established.
func NewDbSQLConnPool(
	ctx context.Context,
	dataSourceName string,
	config Config,
) (*DbSQLConnPool, error) {
	if config.DriverName == "" {
		return nil, fmt.Errorf("DriverName must be specified in config")
	}
	if config.MaxConnections <= 0 {
		return nil, fmt.Errorf("maxConnections must be greater than 0")
	}

	if config.MinIdleConnections <= 0 {
		config.MinIdleConnections = max(config.MaxConnections/4, 1)
	}

	if config.MinIdleConnections > config.MaxConnections {
		return nil, fmt.Errorf(
			"minIdleConnections (%d) cannot exceed maxConnections (%d)",
			config.MinIdleConnections, config.MaxConnections,
		)
	}

	if config.MonitorInterval <= 0 {
		config.MonitorInterval = 1 * time.Minute
	}

	healthCheck := max(config.HealthCheckThreshold, 0)
	if healthCheck == 0 && config.HealthCheckThreshold == 0 {
		// User didn't set it — apply default
		healthCheck = 30 * time.Second
	}

	db, err := sql.Open(config.DriverName, dataSourceName)
	if err != nil {
		return nil, fmt.Errorf(
			"failed to open database %s: %w", dataSourceName, err,
		)
	}

	return &DbSQLConnPool{
		Config:               config,
		ctx:                  ctx,
		pool:                 db,
		connections:          make(chan *CpConn, config.MaxConnections),
		maxConnections:       config.MaxConnections,
		minIdleConnections:   config.MinIdleConnections,
		monitorInterval:      config.MonitorInterval,
		healthCheckThreshold: healthCheck,
		done:                 make(chan struct{}),
	}, nil
}

// DB returns the underlying *sql.DB instance. This method should be used with
// caution, primarily for tools like database migrators that require direct access
// to the *sql.DB object. The connection pool should not be in active use when
// the returned *sql.DB is being manipulated.
func (p *DbSQLConnPool) DB() *sql.DB {
	return p.pool
}

var ct_get, ct_put atomic.Int64

// Get acquires a connection from the pool. It will:
//   - Return an existing idle connection if available (skipping ping if recently used)
//   - Create a new connection if below maxConnections (slot reserved via CAS)
//   - Block waiting for a connection if at maxConnections
//
// Returns ErrPoolClosed if pool has been closed.
func (p *DbSQLConnPool) Get() (*CpConn, error) {
	ct_get.Add(1)
	slog.Debug("Get connection", "total_gets", ct_get.Load(), "num_connections", p.numConnections.Load(), "idle_connections", len(p.connections))
retry:
	if p.closed.Load() {
		return nil, ErrPoolClosed
	}

	// Fast path: try to grab an idle connection without blocking.
	select {
	case cpc := <-p.connections:
		if p.needsHealthCheck(cpc) {
			if err := cpc.Conn.PingContext(p.ctx); err != nil {
				slog.Warn(
					"Idle connection failed ping, closing and retrying",
					"error", err,
				)
				cpc.Close()
				p.numConnections.Add(-1)
				goto retry
			}
		}
		return cpc, nil
	default:
	}

	// No idle connections. Try to reserve a slot atomically (CAS).
	if p.reserveSlot() {
		cpc, err := p.newCpConn()
		if err != nil {
			p.numConnections.Add(-1)
			slog.Warn("Failed to create new connection", "error", err)
			return nil, err
		}
		slog.Debug(
			"New connection created",
			"num_connections", p.numConnections.Load(),
		)
		return cpc, nil
	}

	// At capacity — block until a connection is returned or context cancelled.
	select {
	case cpc := <-p.connections:
		if p.needsHealthCheck(cpc) {
			if err := cpc.Conn.PingContext(p.ctx); err != nil {
				slog.Warn(
					"Blocked-wait connection failed ping, closing and retrying",
					"error", err,
				)
				cpc.Close()
				p.numConnections.Add(-1)
				goto retry
			}
		}
		return cpc, nil
	case <-p.ctx.Done():
		return nil, &ConnectionError{Op: "acquire", Err: p.ctx.Err()}
	}
}

// Put returns a connection to the pool for reuse.
// The connection's idleSince is stamped so that Get() can skip health-check
// pings for recently-returned connections.
// If the pool is closed, the connection is closed immediately.
// Safe to call with nil (no-op).
func (p *DbSQLConnPool) Put(cpc *CpConn) {
	ct_put.Add(1)
	slog.Debug("Put connection", "total_puts", ct_put.Load(), "num_connections", p.numConnections.Load(), "idle_connections", len(p.connections))

	if cpc == nil {
		return
	}

	if p.closed.Load() {
		p.numConnections.Add(-1)
		if err := cpc.Close(); err != nil {
			slog.Error(
				"Error closing connection in closed pool", "err", err,
			)
		}
		return
	}

	cpc.idleSince = time.Now()

	select {
	case p.connections <- cpc:
		// Successfully returned to pool.
	default:
		// Channel full (shouldn't happen with proper sizing).
		cpc.Close()
		p.numConnections.Add(-1)
	}
}

// Close gracefully shuts down the pool and releases all resources.
// Uses atomic CompareAndSwap to guarantee exactly-once shutdown.
// Returns ErrPoolClosed if already closed.
func (p *DbSQLConnPool) Close() error {
	if !p.closed.CompareAndSwap(false, true) {
		return ErrPoolClosed
	}

	// Signal Monitor goroutine to stop.
	close(p.done)

	// Drain and close all idle connections.
	for {
		select {
		case conn := <-p.connections:
			if conn != nil {
				conn.Close()
				p.numConnections.Add(-1)
			}
		default:
			close(p.connections)
			return p.pool.Close()
		}
	}
}

// DbStats returns database/sql DBStats for the underlying sql.DB pool.
func (p *DbSQLConnPool) DbStats() sql.DBStats {
	return p.pool.Stats()
}

// NumIdleConnections returns the current number of idle connections in the pool.
func (p *DbSQLConnPool) NumIdleConnections() int {
	return len(p.connections)
}

// NumConnections returns the current total number of connections in the pool.
func (p *DbSQLConnPool) NumConnections() int64 {
	return p.numConnections.Load()
}

// Monitor maintains the pool's connection count within configured bounds.
// Running as a goroutine, it periodically:
//   - Creates connections up to the full deficit if idle < minIdleConnections
//   - Closes one excess idle connection per tick if idle > minIdleConnections
//
// Exits when context is cancelled or done channel is closed.
func (p *DbSQLConnPool) Monitor() {
	ticker := time.NewTicker(p.monitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-p.done:
			slog.Debug(
				"DbSQLConnPool monitor stopped due to p.done signal.",
			)
			return
		case <-p.ctx.Done():
			slog.Debug(
				"DbSQLConnPool monitor stopped due to context cancellation.",
			)
			return
		case <-ticker.C:
			if p.closed.Load() {
				return
			}

			currentIdle := int64(len(p.connections))
			currentOpen := p.numConnections.Load()

			// Grow: fill up to the full deficit in one tick.
			deficit := p.minIdleConnections - currentIdle
			available := p.maxConnections - currentOpen
			if deficit > available {
				deficit = available
			}

			if deficit > 0 {
				slog.Debug(
					"Monitor: growing pool",
					"deficit", deficit,
					"current_idle", currentIdle,
					"current_open", currentOpen,
				)
			}

			for i := int64(0); i < deficit; i++ {
				if !p.reserveSlot() {
					break
				}
				cpc, err := p.newCpConn()
				if err != nil {
					slog.Error(
						"Monitor: failed to create connection",
						"error", err,
					)
					p.numConnections.Add(-1)
					break // Stop on first failure.
				}
				select {
				case p.connections <- cpc:
				default:
					cpc.Close()
					p.numConnections.Add(-1)
				}
			}

			// Shrink: remove one excess idle connection per tick.
			currentIdle = int64(len(p.connections))
			if currentIdle > p.minIdleConnections {
				select {
				case cpc := <-p.connections:
					cpc.Close()
					p.numConnections.Add(-1)
				default:
				}
			}
		}
	}
}
