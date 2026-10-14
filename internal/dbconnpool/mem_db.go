package dbconnpool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/ncruces/go-sqlite3/driver"

	"github.com/lbe/sfpg-go/internal/testdb"
	"github.com/lbe/sfpg-go/migrations"
)

func useMemDB(dataSourceName string, testUseMemDB bool) bool {
	if strings.TrimSpace(dataSourceName) != "" {
		return false
	}
	return testUseMemDB
}

// memDBLease is the narrow test-database lease surface a slot owns. The
// production factory constructs *testdb.MemoryDatabases; tests inject a
// failing lease to exercise acquisition rollback.
type memDBLease interface {
	MainURI(readOnly bool) string
	ThumbsURI(ctx context.Context, readOnly bool) (string, error)
	Close() error
}

// newMemDBLease constructs the slot lease for schemaKey. Tests replace it
// temporarily and restore the production default in cleanup.
var newMemDBLease = func(ctx context.Context, schemaKey string) (memDBLease, error) {
	return testdb.NewMemoryDatabases(ctx, schemaKey)
}

// memDBSlot owns the memory-database lease for one schema key. The
// underlying images are mode-independent; the slot caches one main and one
// thumbs URI per access mode so RW and RO pools each open their own mode
// while same-mode pools share. The lease is created on first main
// acquisition and closed only on final release.
type memDBSlot struct {
	lease       memDBLease
	mainURI     map[bool]string
	thumbsURI   map[bool]string
	mainReady   bool
	thumbsReady bool
	refs        int
}

// memRegistryMu serializes schema-key slot lifecycle operations (lookup or
// creation, resource readiness, reference increment, final-reference deletion,
// and registry removal) so each is atomic per operation.
var memRegistryMu sync.Mutex

var memMainSlots sync.Map // schemaKey -> *memDBSlot

// memDBSchemaKey resolves the schema key for memory-VFS slot acquisition.
// Tests may replace it temporarily; production uses migrations.LatestSchemaKey.
var memDBSchemaKey = migrations.LatestSchemaKey

// acquireMemSlot ensures the schema-key slot resources required by this
// acquisition are ready and returns the access-mode-specific URIs with a
// release callback. wantThumbs requests the thumbs lease peer; a later
// thumbs-enabled acquisition on a main-only slot materializes thumbs without
// replacing the live main image. Same-mode pools share cached URIs; RW and
// RO pools each receive their own mode. The reference count increments only
// after all required resources are ready. Acquisition is transactional: a
// newly created zero-reference slot is removed on failure, and a thumbs
// failure on a pre-existing referenced slot preserves the main image, slot
// membership, references, and readiness.
func acquireMemSlot(
	ctx context.Context,
	schemaKey string,
	readOnly bool,
	wantThumbs bool,
) (mainURI, thumbsURI string, release func(), err error) {
	memRegistryMu.Lock()
	defer memRegistryMu.Unlock()

	failed := true
	var slot *memDBSlot
	isNew := false
	defer func() {
		if !failed {
			return
		}
		if isNew && slot != nil && slot.refs == 0 {
			if slot.lease != nil {
				_ = slot.lease.Close()
			}
			memMainSlots.Delete(schemaKey)
		}
	}()

	v, _ := memMainSlots.LoadOrStore(schemaKey, &memDBSlot{})
	slot = v.(*memDBSlot)
	if slot.mainURI == nil {
		slot.mainURI = map[bool]string{}
	}
	if slot.thumbsURI == nil {
		slot.thumbsURI = map[bool]string{}
	}
	if slot.lease == nil {
		isNew = true
		lease, lerr := newMemDBLease(ctx, schemaKey)
		if lerr != nil {
			return "", "", nil, fmt.Errorf("create test databases: %w", lerr)
		}
		slot.lease = lease
	}

	if !slot.mainReady {
		slot.mainURI[false] = slot.lease.MainURI(false)
		slot.mainURI[true] = slot.lease.MainURI(true)
		slot.mainReady = true
	}

	if wantThumbs && !slot.thumbsReady {
		rwURI, rwErr := slot.lease.ThumbsURI(ctx, false)
		if rwErr != nil {
			return "", "", nil, fmt.Errorf("materialize thumbs test database: %w", rwErr)
		}
		roURI, roErr := slot.lease.ThumbsURI(ctx, true)
		if roErr != nil {
			return "", "", nil, fmt.Errorf("materialize thumbs test database: %w", roErr)
		}
		slot.thumbsURI[false] = rwURI
		slot.thumbsURI[true] = roURI
		slot.thumbsReady = true
	}

	slot.refs++
	failed = false

	mainURI = slot.mainURI[readOnly]
	if slot.thumbsReady {
		thumbsURI = slot.thumbsURI[readOnly]
	}
	release = func() {
		memRegistryMu.Lock()
		defer memRegistryMu.Unlock()
		s, ok := memMainSlots.Load(schemaKey)
		if !ok || s.(*memDBSlot) != slot {
			return
		}
		cur := s.(*memDBSlot)
		cur.refs--
		if cur.refs > 0 {
			return
		}
		if cur.lease != nil {
			_ = cur.lease.Close()
		}
		memMainSlots.Delete(schemaKey)
	}
	return mainURI, thumbsURI, release, nil
}

type memPoolOpen struct {
	db              *sql.DB
	release         func()
	thumbsAttachURI string
	isMemory        bool
}

// acquireMemSlotForConfig resolves the schema key, then delegates to
// acquireMemSlot. The schema key is resolved through memDBSchemaKey.
func acquireMemSlotForConfig(
	ctx context.Context,
	config Config,
) (string, string, func(), error) {
	schemaKey, err := memDBSchemaKey()
	if err != nil {
		return "", "", nil, err
	}
	return acquireMemSlot(ctx, schemaKey, config.ReadOnly, config.ThumbsDBPath != "")
}

func openMemTestPool(config Config) (*memPoolOpen, error) {
	return openMemTestPoolWithConfig(context.Background(), config)
}

func openMemTestPoolWithConfig(
	ctx context.Context,
	config Config,
) (*memPoolOpen, error) {
	mainURI, thumbsURI, release, err := acquireMemSlotForConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("open test main db: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			release()
		}
	}()

	db, err := driver.Open(mainURI)
	if err != nil {
		return nil, fmt.Errorf("open memory main db: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping memory main db: %w", err)
	}

	out := &memPoolOpen{db: db, release: release, isMemory: true}
	if thumbsURI != "" {
		out.thumbsAttachURI = thumbsURI
	}
	failed = false
	return out, nil
}

var errMemDBLeaseThumbs = errors.New("thumbs test database unavailable")
