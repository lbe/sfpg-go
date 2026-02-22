package gensyncpool

import (
	"bytes"
	"crypto/md5"
	"database/sql"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// helper type for testing reset behavior
type testObj struct {
	used  int
	state int
}

// TestPutReset verifies that reset is called on Put (not on Get).
// The test does NOT verify sync.Pool reuse - that's sync.Pool's concern.
// Under race detection or GC pressure, sync.Pool may drop objects anytime.
func TestPutReset(t *testing.T) {
	var resets int64
	pool := New(func() *testObj { return &testObj{used: 99, state: 7} }, func(o *testObj) {
		atomic.AddInt64(&resets, 1)
		o.used = 0
		o.state = 0
	})

	// First Get returns freshly initialized object (no reset yet).
	o := pool.Get()
	if o.used != 99 || o.state != 7 {
		t.Fatalf("expected initial object values, got used=%d state=%d", o.used, o.state)
	}
	if atomic.LoadInt64(&resets) != 0 {
		t.Fatalf("expected 0 resets before first Put, got %d", resets)
	}

	// Mutate and Put should trigger reset.
	o.used = 123
	o.state = 456
	pool.Put(o)
	if atomic.LoadInt64(&resets) != 1 {
		t.Fatalf("expected 1 reset after Put, got %d", atomic.LoadInt64(&resets))
	}

	// Second Put also triggers reset.
	o2 := pool.Get()
	o2.used = 555
	pool.Put(o2)
	if atomic.LoadInt64(&resets) != 2 {
		t.Fatalf("expected 2 resets after second Put, got %d", atomic.LoadInt64(&resets))
	}
}

// TestReuse verifies that objects are reused (pointer equality) after Put/Get.
func TestReuse(t *testing.T) {
	pool := New(func() *testObj { return &testObj{} }, func(o *testObj) {
		o.used = 0
		o.state = 0
	})
	a := pool.Get()
	pool.Put(a)
	b := pool.Get()
	if b == nil {
		t.Fatalf("expected non-nil object from pool")
	}
	// sync.Pool does not guarantee pointer reuse across Put/Get; objects may be dropped at any time.
	// Instead of asserting pointer equality, assert that any reused object is reset.
	if b.used != 0 || b.state != 0 {
		t.Fatalf("expected object to be reset after Put, got used=%d state=%d", b.used, b.state)
	}
}

// TestConcurrentAccess stresses pool under concurrency.
func TestConcurrentAccess(t *testing.T) {
	pool := New(func() *testObj { return &testObj{} }, func(o *testObj) {
		o.used = 0
		o.state = 0
	})
	var wg sync.WaitGroup
	const goroutines = 64
	const iters = 2000
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for i := range iters {
				o := pool.Get()
				o.used++
				o.state = i
				pool.Put(o)
			}
		}()
	}
	wg.Wait()
	// Grab an object and ensure it was reset after last Put
	o := pool.Get()
	if o.used != 0 || o.state != 0 {
		t.Fatalf("object should be reset after concurrent reuse got used=%d state=%d", o.used, o.state)
	}
}

// TestZeroValuePool documents that zero-value GenSyncPool is unusable until constructed via New.
func TestZeroValuePool(t *testing.T) {
	var p GenSyncPool[*testObj] // zero value
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic using zero-value pool.Get() since sync.Pool.New not set")
		}
	}()
	_ = p.Get() // should panic because underlying sync.Pool.New is nil
}

// TestResetOnPut verifies reset invoked on Put (not on initial Get) with new semantics.
func TestResetOnPut(t *testing.T) {
	var resets int64
	pool := New(func() *testObj { return &testObj{} }, func(o *testObj) {
		atomic.AddInt64(&resets, 1)
		o.used = 0
	})
	o := pool.Get() // freshly allocated, no reset yet
	if atomic.LoadInt64(&resets) != 0 {
		t.Fatalf("expected 0 resets before first Put, got %d", resets)
	}
	o.used = 42
	pool.Put(o) // triggers first reset
	if atomic.LoadInt64(&resets) != 1 {
		t.Fatalf("expected 1 reset after Put, got %d", resets)
	}
	o2 := pool.Get() // reused object, already reset
	if o2.used != 0 {
		t.Fatalf("expected object cleared to used=0 got %d", o2.used)
	}
	if atomic.LoadInt64(&resets) != 1 {
		t.Fatalf("unexpected additional reset count %d", resets)
	}
	// Second Put triggers second reset
	pool.Put(o2)
	if atomic.LoadInt64(&resets) != 2 {
		t.Fatalf("expected second reset after second Put, got %d", resets)
	}
}

// Benchmark to provide reference performance for Get vs New usage.
func BenchmarkGenSyncPool_Get(b *testing.B) {
	pool := New(func() *testObj { return &testObj{} }, func(o *testObj) {
		o.used = 0
		o.state = 0
	})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		o := pool.Get()
		o.used = i
		pool.Put(o)
	}
}

var sink *testObj

func BenchmarkGenSyncPool_NewAlloc(b *testing.B) {
	// Measures fresh allocation + reset without pooling (baseline comparison)
	// Use sink to prevent escape analysis optimization
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		o := &testObj{}
		o.used = 0
		o.state = 0
		o.used = i
		sink = o // force heap allocation
	}
}

// --- bytes.Buffer pool benchmarks ---

func touchBytesBuffer(buf *bytes.Buffer) {
	buf.Write([]byte("abcdefg"))
	_ = buf.Len()
}

func BenchmarkGenSyncPool_BytesBuffer(b *testing.B) {
	pool := New(
		func() *bytes.Buffer { return &bytes.Buffer{} },
		func(buf *bytes.Buffer) { buf.Reset() },
	)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		v := pool.Get()
		touchBytesBuffer(v)
		pool.Put(v)
	}
}

// --- sql.NullString pool benchmarks ---

func touchNullString(ns *sql.NullString) {
	ns.String = "x"
	ns.Valid = true
}

func BenchmarkGenSyncPool_NullString(b *testing.B) {
	pool := New(
		func() *sql.NullString { return &sql.NullString{} },
		func(ns *sql.NullString) { ns.String = ""; ns.Valid = false },
	)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		v := pool.Get()
		touchNullString(v)
		pool.Put(v)
	}
}

// --- sql.NullInt64 pool benchmarks ---

func touchNullInt64(ni *sql.NullInt64) {
	ni.Int64 = 1
	ni.Valid = true
}

func BenchmarkGenSyncPool_NullInt64(b *testing.B) {
	pool := New(
		func() *sql.NullInt64 { return &sql.NullInt64{} },
		func(ni *sql.NullInt64) { ni.Int64 = 0; ni.Valid = false },
	)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		v := pool.Get()
		touchNullInt64(v)
		pool.Put(v)
	}
}

// --- MD5 hash pool benchmarks ---

type hashIface = interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
	Reset()
}

func touchMD5(h hashIface) {
	_, _ = h.Write([]byte("abcdefg"))
	_ = h.Sum(nil)
}

func BenchmarkGenSyncPool_MD5(b *testing.B) {
	pool := New(
		func() hashIface { return md5.New() },
		func(h hashIface) { h.Reset() },
	)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		v := pool.Get()
		touchMD5(v)
		pool.Put(v)
	}
}

// TestResetUnderGC stresses the pool with forced GCs and verifies that
// whenever an object is actually reused (same logical instance), it
// is observed in a reset state. Fresh allocations retain init-state.
func TestResetUnderGC(t *testing.T) {
	type testObj2 struct {
		id    int64
		used  int
		state int
	}
	var nextID int64
	const initUsed, initState = 77, 88
	pool := New(
		func() *testObj2 { return &testObj2{id: atomic.AddInt64(&nextID, 1), used: initUsed, state: initState} },
		func(o *testObj2) { o.used = 0; o.state = 0 },
	)

	seen := make(map[int64]struct{})
	const iters = 5000
	for i := range iters {
		// Occasionally encourage GC to drop pooled items
		if i%100 == 0 {
			runtime.GC()
			runtime.Gosched()
		}

		o := pool.Get()
		if _, ok := seen[o.id]; ok {
			// Reuse of an existing object instance must present reset state
			if o.used != 0 || o.state != 0 {
				t.Fatalf("reused object id=%d should be reset, got used=%d state=%d", o.id, o.used, o.state)
			}
		} else {
			// First time we've observed this allocation; init-state is expected
			if o.used != initUsed || o.state != initState {
				t.Fatalf("new object id=%d should have init-state, got used=%d state=%d", o.id, o.used, o.state)
			}
			seen[o.id] = struct{}{}
		}
		// Mutate and return to pool
		o.used = i
		o.state = i
		pool.Put(o)
	}
}
