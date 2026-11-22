package discovery

import (
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"

	"github.com/lbe/sfpg-go/internal/dque"
	"github.com/lbe/sfpg-go/internal/queue"
)

// itemsPerSegment is the number of items in each backlog segment file.
const itemsPerSegment = 1000

// PathWork is one queued file: its path and the mtime and size the walk saw.
type PathWork struct {
	Path      []byte
	MtimeUnix int64
	SizeBytes int64
}

// GobEncode encodes w for the backlog: big-endian mtime, size and path length,
// then the path.
func (w PathWork) GobEncode() ([]byte, error) {
	buf := make([]byte, 8+8+4+len(w.Path))
	binary.BigEndian.PutUint64(buf[0:8], uint64(w.MtimeUnix))
	binary.BigEndian.PutUint64(buf[8:16], uint64(w.SizeBytes))
	binary.BigEndian.PutUint32(buf[16:20], uint32(len(w.Path)))
	copy(buf[20:], w.Path)
	return buf, nil
}

// GobDecode decodes w from the backlog.
func (w *PathWork) GobDecode(data []byte) error {
	if len(data) < 20 {
		return fmt.Errorf("discovery path work: short gob payload (%d bytes)", len(data))
	}
	w.MtimeUnix = int64(binary.BigEndian.Uint64(data[0:8]))
	w.SizeBytes = int64(binary.BigEndian.Uint64(data[8:16]))
	end := 20 + int(binary.BigEndian.Uint32(data[16:20]))
	if len(data) < end {
		return fmt.Errorf("discovery path work: truncated path in gob payload")
	}
	w.Path = slices.Clone(data[20:end])
	return nil
}

// backlog is the disk queue of files the walk found and the workers have not
// taken yet.
type backlog struct {
	dq *dque.DQue[PathWork]
}

// openBacklog opens a new, empty backlog in dir. Anything in dir is removed:
// a full walk is the recovery from an interrupted run.
func openBacklog(dir string) (*backlog, error) {
	if err := os.RemoveAll(dir); err != nil {
		return nil, fmt.Errorf("discovery dque: wipe %s: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("discovery dque: create parent dir %s: %w", dir, err)
	}
	dq, err := dque.New[PathWork]("discovery", dir, itemsPerSegment)
	if err != nil {
		return nil, fmt.Errorf("discovery dque: open queue in %s: %w", dir, err)
	}
	if err := dq.TurboOn(); err != nil {
		return nil, errors.Join(fmt.Errorf("discovery dque: enable turbo in %s: %w", dir, err), dq.Close())
	}
	return &backlog{dq: dq}, nil
}

func (b *backlog) enqueue(w PathWork) error {
	if err := b.dq.Enqueue(&w); err != nil {
		if errors.Is(err, dque.ErrQueueClosed) {
			return queue.ErrClosedQueue
		}
		return err
	}
	return nil
}

// dequeue returns the next item, queue.ErrEmptyQueue or queue.ErrClosedQueue.
// dque can return an item together with a segment cleanup error; the item
// was removed, so it is returned and the error logged.
func (b *backlog) dequeue() (PathWork, error) {
	w, err := b.dq.Dequeue()
	switch {
	case w != nil:
		if err != nil {
			slog.Error("discovery dque: item dequeued despite cleanup error; continuing with item", "err", err)
		}
		return *w, nil
	case errors.Is(err, dque.ErrEmpty):
		return PathWork{}, queue.ErrEmptyQueue
	case errors.Is(err, dque.ErrQueueClosed):
		return PathWork{}, queue.ErrClosedQueue
	}
	return PathWork{}, err
}

func (b *backlog) len() int { return b.dq.Size() }

func (b *backlog) close() error {
	if err := b.dq.Close(); err != nil && !errors.Is(err, dque.ErrQueueClosed) {
		return fmt.Errorf("discovery dque: close: %w", err)
	}
	return nil
}
