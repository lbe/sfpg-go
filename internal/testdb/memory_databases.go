// Package testdb provides the single temporary, memory-backed test database
// mechanism for this repository. A MemoryDatabases instance reserves a fresh
// main/thumbs memdb VFS pair for a caller-supplied schema key: construction
// materializes and restores main immediately, while ThumbsURI materializes
// and restores its fresh thumbs peer once on first use.
//
// Seeds are process-local implementation state: built or rebuilt from
// migrations under one process-temporary root, checkpointed and normalized
// to rollback-journal mode, with sidecars removed. Callers receive only
// SQLite URIs, never file paths.
package testdb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/vfs/memdb"

	"github.com/lbe/sfpg-go/internal/flock"
	"github.com/lbe/sfpg-go/migrations"
)

var (
	seedsRootOnce sync.Once
	seedsRoot     string
	seedsRootErr  error

	seedsMu sync.Mutex
)

// MemoryDatabases reserves a fresh main/thumbs memdb VFS image pair for one
// schema key. Construction materializes main; ThumbsURI materializes thumbs
// once on first use. Close is idempotent and releases every materialized
// image.
type MemoryDatabases struct {
	schemaKey string

	mainName   string
	thumbsName string
	mainReady  bool

	thumbsOnce sync.Once
	thumbsErr  error
	thumbsMu   sync.Mutex

	closeOnce sync.Once
	closeErr  error
	closed    bool
	mu        sync.Mutex
}

// NewMemoryDatabases materializes a fresh main memory image for schemaKey
// and reserves its fresh thumbs peer. schemaKey selects only internal
// process-local seeds; it must not be inferred from a caller path.
func NewMemoryDatabases(ctx context.Context, schemaKey string) (*MemoryDatabases, error) {
	if strings.TrimSpace(schemaKey) == "" {
		return nil, errors.New("testdb: schema key must not be empty")
	}
	m := &MemoryDatabases{schemaKey: schemaKey}
	mainName := nextImageName("sfpg-testdb-main", schemaKey)
	if err := restoreSeedToImage(ctx, "main", mainName, schemaKey); err != nil {
		return nil, err
	}
	m.mainName = mainName
	m.mainReady = true
	return m, nil
}

// MainURI returns the already-materialized main image URI. readOnly selects
// the URI access mode; callers must never assemble SQLite URIs themselves.
func (m *MemoryDatabases) MainURI(readOnly bool) string {
	return imageURI(m.mainName, readOnly, true)
}

// ThumbsURI materializes the reserved thumbs peer once on first use and
// then returns the same URI. readOnly selects the URI access mode.
func (m *MemoryDatabases) ThumbsURI(ctx context.Context, readOnly bool) (string, error) {
	m.thumbsOnce.Do(func() {
		name := nextImageName("sfpg-testdb-thumbs", m.schemaKey)
		if err := restoreSeedToImage(ctx, "thumbs", name, m.schemaKey); err != nil {
			m.thumbsErr = err
			return
		}
		m.thumbsMu.Lock()
		m.thumbsName = name
		m.thumbsMu.Unlock()
	})
	if m.thumbsErr != nil {
		return "", m.thumbsErr
	}
	m.thumbsMu.Lock()
	name := m.thumbsName
	m.thumbsMu.Unlock()
	return imageURI(name, readOnly, false), nil
}

// Close is idempotent: it releases every materialized image and a
// subsequent constructor call yields a fresh pair.
func (m *MemoryDatabases) Close() error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		mainName, mainReady := m.mainName, m.mainReady
		m.mu.Unlock()
		m.thumbsMu.Lock()
		thumbsName := m.thumbsName
		m.thumbsMu.Unlock()
		if mainReady {
			memdb.Delete(mainName)
		}
		if thumbsName != "" {
			memdb.Delete(thumbsName)
		}
	})
	return m.closeErr
}

var imageCounter int64
var imageCounterMu sync.Mutex

func nextImageName(prefix, schemaKey string) string {
	imageCounterMu.Lock()
	imageCounter++
	n := imageCounter
	imageCounterMu.Unlock()
	return fmt.Sprintf("%s-%d-%s-%d", prefix, os.Getpid(), sanitizeName(schemaKey), n)
}

func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	if b.Len() == 0 {
		return "db"
	}
	return b.String()
}

// imageURI builds a named memdb VFS URI. Each name passed to memdb.Create
// excludes the leading '/'; each URI path includes it. readOnly selects
// mode=ro versus mode=rw (plus _txlock=immediate on main).
func imageURI(name string, readOnly, isMain bool) string {
	q := url.Values{}
	q.Set("vfs", "memdb")
	if readOnly {
		q.Set("mode", "ro")
	} else {
		q.Set("mode", "rw")
		if isMain {
			q.Set("_txlock", "immediate")
		}
	}
	u := url.URL{Scheme: "file", OmitHost: true, Path: "/" + name, RawQuery: q.Encode()}
	return u.String()
}

func seedsDir() (string, error) {
	seedsRootOnce.Do(func() {
		seedsRoot, seedsRootErr = os.MkdirTemp("", "sfpg-testdb-")
	})
	if seedsRootErr != nil {
		return "", seedsRootErr
	}
	dir := filepath.Join(seedsRoot, "seeds")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func mainSeedPath(schemaKey string) (string, error) {
	dir, err := seedsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sfpg-go-"+schemaKey+".db"), nil
}

func thumbsSeedPath(schemaKey string) (string, error) {
	dir, err := seedsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "thumbs-"+schemaKey+".db"), nil
}

func fileURI(path string) string {
	return "file:" + filepath.ToSlash(path)
}

func fileURIReadOnly(path string) string {
	return fileURI(path) + "?mode=ro"
}

func querySingleText(conn *sqlite3.Conn, query string) (string, error) {
	stmt, _, err := conn.Prepare(query)
	if err != nil {
		return "", err
	}
	defer stmt.Close()
	if !stmt.Step() {
		if err := stmt.Err(); err != nil {
			return "", err
		}
		return "", fmt.Errorf("no rows for %q", query)
	}
	return stmt.ColumnText(0), nil
}

func querySeedJournalMode(path string) (string, error) {
	conn, err := sqlite3.OpenContext(context.Background(), fileURIReadOnly(path))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	mode, err := querySingleText(conn, "PRAGMA journal_mode")
	if err != nil {
		return "", err
	}
	return strings.ToLower(strings.TrimSpace(mode)), nil
}

func removeSidecars(path string) {
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
}

func seedReady(path string) bool {
	mode, err := querySeedJournalMode(path)
	if err != nil {
		return false
	}
	return mode == "delete"
}

func finalizeSeedTmp(ctx context.Context, tmp, path string) error {
	conn, err := sqlite3.OpenContext(ctx, fileURI(tmp)+"?mode=rw")
	if err != nil {
		return err
	}
	checkpointErr := func() error {
		defer conn.Close()
		if err := conn.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			return err
		}
		if err := conn.Exec("PRAGMA journal_mode=DELETE"); err != nil {
			return err
		}
		mode, err := querySingleText(conn, "PRAGMA journal_mode")
		if err != nil {
			return err
		}
		if !strings.EqualFold(strings.TrimSpace(mode), "delete") {
			return fmt.Errorf("testdb seed %q journal_mode = %q, want delete", tmp, mode)
		}
		return nil
	}()
	if checkpointErr != nil {
		_ = os.Remove(tmp)
		removeSidecars(tmp)
		return checkpointErr
	}
	removeSidecars(tmp)
	removeSidecars(path)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func withFileLock(lockPath string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	fl := flock.New(lockPath)
	defer fl.Close()
	for {
		ok, err := fl.TryLock()
		if err != nil {
			return err
		}
		if ok {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fn()
}

func ensureMainSeed(schemaKey string) error {
	path, err := mainSeedPath(schemaKey)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(path); statErr == nil && seedReady(path) {
		return nil
	}
	lockPath := path + ".lock"
	return withFileLock(lockPath, func() error {
		if _, statErr := os.Stat(path); statErr == nil {
			if seedReady(path) {
				return nil
			}
			_ = os.Remove(path)
			removeSidecars(path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		tmp := path + ".tmp"
		_ = os.Remove(tmp)
		removeSidecars(tmp)

		d, err := iofs.New(migrations.FS, "migrations")
		if err != nil {
			return err
		}
		m, err := migrate.NewWithSourceInstance("iofs", d, "sqlite://"+filepath.ToSlash(tmp))
		if err != nil {
			return err
		}
		upErr := m.Up()
		if upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
			m.Close()
			_ = os.Remove(tmp)
			removeSidecars(tmp)
			return upErr
		}
		if _, err := m.Close(); err != nil {
			_ = os.Remove(tmp)
			removeSidecars(tmp)
			return err
		}
		return finalizeSeedTmp(context.Background(), tmp, path)
	})
}

func ensureThumbsSeed(schemaKey string) error {
	path, err := thumbsSeedPath(schemaKey)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(path); statErr == nil && seedReady(path) {
		return nil
	}
	lockPath := path + ".lock"
	return withFileLock(lockPath, func() error {
		if _, statErr := os.Stat(path); statErr == nil {
			if seedReady(path) {
				return nil
			}
			_ = os.Remove(path)
			removeSidecars(path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		tmp := path + ".tmp"
		_ = os.Remove(tmp)
		removeSidecars(tmp)

		m, err := migrations.NewThumbsMigrator(tmp)
		if err != nil {
			return err
		}
		upErr := m.Up()
		if upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
			_, _ = m.Close()
			_ = os.Remove(tmp)
			removeSidecars(tmp)
			return upErr
		}
		if _, err := m.Close(); err != nil {
			_ = os.Remove(tmp)
			removeSidecars(tmp)
			return err
		}
		return finalizeSeedTmp(context.Background(), tmp, path)
	})
}

// restoreSeedToImage creates an empty named memdb image, restores the seed
// file for kind ("main" or "thumbs") into it exactly once via Conn.Restore,
// and verifies memory journal mode. On any error the image is deleted and
// the seed key is left retryable.
func restoreSeedToImage(ctx context.Context, kind, name, schemaKey string) error {
	var seedPath string
	var ensureErr error
	seedsMu.Lock()
	switch kind {
	case "main":
		ensureErr = ensureMainSeed(schemaKey)
		if ensureErr == nil {
			seedPath, ensureErr = mainSeedPath(schemaKey)
		}
	case "thumbs":
		ensureErr = ensureThumbsSeed(schemaKey)
		if ensureErr == nil {
			seedPath, ensureErr = thumbsSeedPath(schemaKey)
		}
	default:
		ensureErr = fmt.Errorf("testdb: unknown seed kind %q", kind)
	}
	seedsMu.Unlock()
	if ensureErr != nil {
		return ensureErr
	}

	memdb.Create(name, nil)
	cleanup := true
	defer func() {
		if cleanup {
			memdb.Delete(name)
		}
	}()

	dst, err := sqlite3.OpenContext(ctx, imageURI(name, false, true))
	if err != nil {
		return err
	}
	defer dst.Close()
	if rerr := dst.Restore("main", fileURIReadOnly(seedPath)); rerr != nil {
		return rerr
	}
	if jerr := dst.Exec("PRAGMA journal_mode=MEMORY"); jerr != nil {
		return jerr
	}
	mode, err := querySingleText(dst, "PRAGMA journal_mode")
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(mode), "memory") {
		return fmt.Errorf("testdb image %q journal_mode = %q, want memory", name, mode)
	}
	cleanup = false
	return nil
}
