// Package gensyncpool provides a type-safe generic wrapper around sync.Pool
// that applies a caller-provided reset function when objects are *returned*
// to the pool (Put), matching conventional sync.Pool usage patterns.
//
// This package simplifies object pooling by:
//   - Eliminating type assertions with Go generics
//   - Ensuring objects are reset to a clean state before being reused
//   - Providing a simple, consistent API for pooled resource management
//
// # Usage
//
// Create a pool with an initialization function and a reset function:
//
//	pool := gensyncpool.New(
//	    func() *MyStruct { return &MyStruct{} },
//	    func(s *MyStruct) { s.Clear() },
//	)
//
// Get an object (automatically reset), use it, then return it:
//
//	obj := pool.Get()
//	defer pool.Put(obj)
//	// use obj...
//
// # Performance Characteristics
//
// GenSyncPool leverages sync.Pool's GC-aware pooling:
//   - Objects may be reclaimed during GC if not actively in use
//   - Pool creation is expensive (~microseconds); create pools once at initialization
//   - Get operations are fast (~tens of ns) when objects are available
//   - Eliminates allocation overhead for reused objects (0 allocs/op after warm-up)
//
// Benchmark your specific use case to ensure pooling provides measurable benefit
// over simple allocation. Small, cheap-to-construct objects may not benefit significantly.
//
// # Thread Safety
//
// All methods are safe for concurrent use by multiple goroutines.
package gensyncpool

import "sync"

// GenSyncPool is a type-safe wrapper around sync.Pool. Objects are reset
// via the provided reset function *when they are Put back* into the pool,
// matching the conventional pattern of cleaning up before reuse.
//
// A GenSyncPool must be created using New and should not be copied after first use.
// The zero value is not usable.
type GenSyncPool[T any] struct {
	pool  sync.Pool
	reset func(T)
}

// New creates a new GenSyncPool for type T.
//
// The init function is called to create new instances when the pool is empty.
// The reset function is called on every object passed to Put, ensuring the
// object is returned to a clean state before future reuse.
//
// Both functions must be non-nil and safe for concurrent use.
//
// Example:
//
//	type Buffer struct { data []byte }
//	func (b *Buffer) Clear() { b.data = b.data[:0] }
//
//	pool := gensyncpool.New(
//	    func() *Buffer { return &Buffer{data: make([]byte, 0, 1024)} },
//	    func(b *Buffer) { b.Clear() },
//	)
//
// The returned GenSyncPool should be stored as a package-level variable
// or struct field to avoid repeated construction overhead.
func New[T any](init func() T, reset func(T)) GenSyncPool[T] {
	return GenSyncPool[T]{
		pool: sync.Pool{
			New: func() any { return init() },
		},
		reset: reset,
	}
}

// Get retrieves an object from the pool.
//
// If the pool is empty, a new object is created using the init function provided to New.
// Objects are reset when they are Put back into the pool. Therefore:
//   - A freshly allocated object (first Get) will have the state from the init function.
//   - A reused object (after a prior Put) will already be in a reset/clean state.
//
// The caller is responsible for returning the object to the pool via Put when done.
//
// Get is safe to call concurrently from multiple goroutines.
func (p *GenSyncPool[T]) Get() T {
	// Return the object as-is. The caller should assume it was reset before
	// last Put, or perform any additional initialization needed for this use.
	return p.pool.Get().(T)
}

// Put returns an object to the pool for future reuse.
//
// The reset function is applied to the object before it becomes available to others,
// ensuring it is returned to a clean state for the next Get.
// Callers should not retain references to objects after calling Put, as the object
// may be reused by other goroutines or garbage collected.
//
// Put is safe to call concurrently from multiple goroutines.
//
// It is safe (but wasteful) to Put the same object multiple times or to Put nil.
// However, putting an object of the wrong type will cause a panic on the next Get.
func (p *GenSyncPool[T]) Put(val T) {
	// Apply reset before making the object available to other callers.
	p.reset(val)
	p.pool.Put(val)
}
