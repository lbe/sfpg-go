package server

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/lbe/sfpg-go/internal/dque"
	"github.com/lbe/sfpg-go/internal/queue"
)

// discoveryDQueItemsPerSegment is the number of items stored in each dque
// segment file for the dedicated discovery backlog queue.
const discoveryDQueItemsPerSegment = 1000

// discoveryDQueAdapter adapts a *dque.DQue[string] to the queue.Queuer[string]
// contract used by the discovery walker and its file workers.
//
// The adapter lives in the server package (not internal/queue) so that
// internal/queue never depends on internal/dque.
type discoveryDQueAdapter struct {
	dq *dque.DQue[string]
}

// Compile-time assertion that the adapter satisfies the discovery queue contract.
var _ queue.Queuer[string] = (*discoveryDQueAdapter)(nil)

// newDiscoveryDQueAdapter opens (or creates) a dedicated discovery dque at
// parentDir and returns it as a queue.Queuer[string]. The parent directory is
// created first (WriteBatcher pattern) and turbo mode is enabled. parentDir
// must be a dedicated directory, not a wipe root shared with other state.
func newDiscoveryDQueAdapter(parentDir string) (queue.Queuer[string], error) {
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return nil, fmt.Errorf("discovery dque: create parent dir %s: %w", parentDir, err)
	}
	dq, err := dque.New[string]("discovery", parentDir, discoveryDQueItemsPerSegment)
	if err != nil {
		return nil, fmt.Errorf("discovery dque: open queue in %s: %w", parentDir, err)
	}
	if err := dq.TurboOn(); err != nil {
		if closeErr := dq.Close(); closeErr != nil {
			return nil, fmt.Errorf("discovery dque: enable turbo in %s: %w (close after turbo failure: %w)", parentDir, err, closeErr)
		}
		return nil, fmt.Errorf("discovery dque: enable turbo in %s: %w", parentDir, err)
	}
	return &discoveryDQueAdapter{dq: dq}, nil
}

// Enqueue appends an item to the discovery backlog.
func (d *discoveryDQueAdapter) Enqueue(item string) error {
	if err := d.dq.Enqueue(&item); err != nil {
		if errors.Is(err, dque.ErrQueueClosed) {
			return queue.ErrClosedQueue
		}
		return err
	}
	return nil
}

// Dequeue removes and returns the front item of the discovery backlog.
//
// dque may return a non-nil item together with a non-Empty/Closed cleanup
// error (the item was removed but segment cleanup failed). The adapter logs
// that cleanup error but keeps the item: workers must keep processing it
// rather than drop it. The mapping itself lives in mapDiscoveryDequeueResult.
func (d *discoveryDQueAdapter) Dequeue() (string, error) {
	ptr, err := d.dq.Dequeue()
	if ptr != nil && err != nil &&
		!errors.Is(err, dque.ErrEmpty) && !errors.Is(err, dque.ErrQueueClosed) {
		slog.Error("discovery dque: item dequeued despite cleanup error; continuing with item", "err", err)
	}
	return mapDiscoveryDequeueResult(ptr, err)
}

// Len returns the number of items currently in the discovery backlog.
func (d *discoveryDQueAdapter) Len() int {
	return d.dq.Size()
}

// IsEmpty reports whether the discovery backlog holds no items.
func (d *discoveryDQueAdapter) IsEmpty() bool {
	return d.Len() == 0
}

// Close releases the discovery dque. It is idempotent: a second Close is a
// no-op (dque.ErrQueueClosed is swallowed). Other close errors are logged.
func (d *discoveryDQueAdapter) Close() {
	if err := d.dq.Close(); err != nil && !errors.Is(err, dque.ErrQueueClosed) {
		slog.Warn("discovery dque: close failed", "err", err)
	}
}

// mapDiscoveryDequeueResult maps a dque.Dequeue result to the queue.Queuer
// contract. It is pure: it performs no logging and has no side effects.
//
// dque documents that Dequeue may return a non-nil item together with a
// cleanup error (segment deletion/creation failed after the item was already
// removed). Such items are not dropped: the value is returned with a nil error
// so workers keep processing it. The adapter's Dequeue is responsible for
// logging that cleanup error.
func mapDiscoveryDequeueResult(ptr *string, err error) (string, error) {
	if ptr != nil {
		return *ptr, nil
	}
	switch {
	case errors.Is(err, dque.ErrEmpty):
		return "", queue.ErrEmptyQueue
	case errors.Is(err, dque.ErrQueueClosed):
		return "", queue.ErrClosedQueue
	default:
		return "", err
	}
}
