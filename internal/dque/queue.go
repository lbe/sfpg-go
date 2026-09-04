// Package dque is a fast, embedded, type-safe durable queue for Go.  DQue[T any]
// provides a generic FIFO queue backed by disk segments, eliminating the need for
// manual type assertions on Dequeue/Peek return values.
package dque

//
// Copyright (c) 2018 Jon Carlson.  All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.
//

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/lbe/sfpg-go/internal/errors"
	"github.com/lbe/sfpg-go/internal/flock"
)

const lockFile = "lock.lock"

// ErrQueueClosed is the error returned when a queue is closed.
var ErrQueueClosed = errors.New("queue is closed")

var (
	filePattern *regexp.Regexp

	// ErrEmpty is returned when attempting to dequeue from an empty queue.
	ErrEmpty = errors.New("dque is empty")
)

func init() {
	filePattern = regexp.MustCompile(`^([0-9]+)\.dque$`)
}

type config struct {
	ItemsPerSegment int
}

// DQue is the in-memory representation of a type-safe queue on disk.  You must
// never have two *active* DQue instances pointing at the same path on disk.  It
// is acceptable to reconstitute a new instance from disk, but make sure the old
// instance is never enqueued to (or dequeued from) again.
type DQue[T any] struct {
	Name    string
	DirPath string
	config  config

	fullPath     string
	fileLock     *flock.Flock
	firstSegment *qSegment[T]
	lastSegment  *qSegment[T]

	mutex sync.Mutex

	emptyCond *sync.Cond

	// cachedDiskBytes is the cached sum of all non-directory file sizes in the
	// queue directory (lock.lock plus every segment file). It is seeded once
	// after the queue is loaded and maintained on enqueue/dequeue so that
	// DiskBytes() is O(1) and never re-reads the directory.
	cachedDiskBytes int64

	turbo bool

	// closed is set to true when Close() is called and is used by unsynchronized
	// readers to avoid data races on the segment and lock fields.
	closed bool
}

// validateQueueInputs validates the common inputs for all constructors and
// returns the full path to the queue directory. It rejects unsafe queue names
// (empty, path separators, '.'/'..', or '..' segments) and invalid
// itemsPerSegment values.
func validateQueueInputs(name, dirPath string, itemsPerSegment int) (string, error) {
	if len(name) == 0 {
		return "", errors.New("the queue name requires a value")
	}
	if name == "." || name == ".." {
		return "", errors.New("the queue name cannot be '.' or '..'")
	}
	// Reject any path separators so the queue cannot escape dirPath.
	if strings.ContainsAny(name, string(os.PathSeparator)+"/\\") {
		return "", errors.New("the queue name cannot contain path separators")
	}
	if len(dirPath) == 0 {
		return "", errors.New("the queue directory requires a value")
	}
	if exists, err := dirExists(dirPath); err != nil {
		return "", fmt.Errorf("checking directory %s: %w", dirPath, err)
	} else if !exists {
		return "", errors.New("the given queue directory is not valid: " + dirPath)
	}
	if itemsPerSegment <= 0 {
		return "", errors.New("itemsPerSegment must be greater than zero")
	}
	return filepath.Join(dirPath, name), nil
}

// createQueueDir ensures the queue directory exists (or does not exist),
// creating it when mustCreate is true. It returns a descriptive error
// when the precondition is violated or when the filesystem operation fails.
func createQueueDir(fullPath string, mustCreate bool) error {
	exists, err := dirExists(fullPath)
	if err != nil {
		return err
	}
	if mustCreate && exists {
		return errors.New("the given queue directory already exists: " + fullPath + ". Use Open instead")
	}
	if !mustCreate && !exists {
		return errors.New("the given queue does not exist (" + fullPath + ")")
	}
	if mustCreate {
		return os.Mkdir(fullPath, 0755)
	}
	return nil
}

// New creates a new durable queue for items of type T.
func New[T any](name string, dirPath string, itemsPerSegment int) (*DQue[T], error) {
	fullPath, err := validateQueueInputs(name, dirPath, itemsPerSegment)
	if err != nil {
		return nil, err
	}
	if err := createQueueDir(fullPath, true); err != nil {
		return nil, err
	}
	q := &DQue[T]{Name: name, DirPath: dirPath}
	if err := q.initQueue(fullPath, itemsPerSegment); err != nil {
		return nil, err
	}
	return q, nil
}

// Open opens an existing durable queue for items of type T.
func Open[T any](name string, dirPath string, itemsPerSegment int) (*DQue[T], error) {
	fullPath, err := validateQueueInputs(name, dirPath, itemsPerSegment)
	if err != nil {
		return nil, err
	}
	if err := createQueueDir(fullPath, false); err != nil {
		return nil, err
	}
	q := &DQue[T]{Name: name, DirPath: dirPath}
	if err := q.initQueue(fullPath, itemsPerSegment); err != nil {
		return nil, err
	}
	return q, nil
}

// NewOrOpen either creates a new queue for items of type T, or opens an existing
// durable queue.
func NewOrOpen[T any](name string, dirPath string, itemsPerSegment int) (*DQue[T], error) {

	fullPath, err := validateQueueInputs(name, dirPath, itemsPerSegment)
	if err != nil {
		return nil, err
	}
	if exists, err := dirExists(fullPath); err != nil {
		return nil, fmt.Errorf("checking directory %s: %w", fullPath, err)
	} else if exists {
		return Open[T](name, dirPath, itemsPerSegment)
	}

	return New[T](name, dirPath, itemsPerSegment)
}

// Close releases the lock on the queue rendering it unusable for further usage by this instance.
// Close will return an error if it has already been called.
func (q *DQue[T]) Close() error {
	// only allow Close while no other function is active
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if q.closed {
		return ErrQueueClosed
	}

	// Close the first and last segments' file handles, collecting all errors.
	var closeErr error
	if err := q.firstSegment.close(); err != nil {
		closeErr = errors.Join(closeErr, err)
	}
	if q.firstSegment != q.lastSegment {
		if err := q.lastSegment.close(); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}

	// Release the lock and close the lock file descriptor.
	if err := q.fileLock.Close(); err != nil {
		closeErr = errors.Join(closeErr, err)
	}

	// Mark the instance as closed first, so any unsynchronized readers
	// see the closed state before the segments are nil-ed.
	q.closed = true

	// Safe-guard ourself from accidentally using segments after closing the queue
	q.firstSegment = nil
	q.lastSegment = nil
	q.fileLock = nil
	q.emptyCond.Broadcast()

	return closeErr
}

// Enqueue adds an item to the end of the queue
func (q *DQue[T]) Enqueue(obj *T) error {
	// This is heavy-handed but its safe
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if q.closed {
		return ErrQueueClosed
	}

	// If this segment is full then create a new one
	if q.lastSegment.sizeOnDisk() >= q.config.ItemsPerSegment {

		// We have filled our last segment to capacity, so create a new one
		seg, err := newQueueSegment[T](q.fullPath, q.lastSegment.number+1, q.turbo)
		if err != nil {
			return errors.Wrapf(err, "error creating new queue segment: %d.", q.lastSegment.number+1)
		}

		// If the last segment is not the first segment
		// then we need to close the file.
		if q.firstSegment != q.lastSegment {
			var err = q.lastSegment.close()
			if err != nil {
				return errors.Wrapf(err, "error closing previous segment file #%d.", q.lastSegment.number)
			}
		}

		// Replace the last segment with the new one
		q.lastSegment = seg

	}

	// Remember the last segment's size before adding so we can track the growth.
	oldSize := sizeOfFile(q.lastSegment.filePath())

	// Add the item to the last segment
	if err := q.lastSegment.add(obj); err != nil {
		return errors.Wrap(err, "error adding item to the last segment")
	}

	// Maintain the cached disk total by the growth of this one segment file
	// (newSize - oldSize). oldSize is 0 when this Enqueue created a new segment
	// file, so this adds only the new file's bytes. Do NOT assign Size() (drops
	// lock.lock and other segments) and do NOT += Size() (double-counts).
	q.cachedDiskBytes += sizeOfFile(q.lastSegment.filePath()) - oldSize

	// Wakeup any goroutine that is currently waiting for an item to be enqueued
	q.emptyCond.Broadcast()

	return nil
}

// Dequeue removes and returns the first item in the queue.
// When the queue is empty, nil and dque.ErrEmpty are returned.
//
// On error, the returned object may still be non-nil and valid — it was
// successfully dequeued but subsequent cleanup (segment deletion or creation)
// failed. Callers should process the returned object even when err != nil.
func (q *DQue[T]) Dequeue() (*T, error) {
	// This is heavy-handed but its safe
	q.mutex.Lock()
	defer q.mutex.Unlock()

	return q.dequeueLocked()
}

func (q *DQue[T]) dequeueLocked() (*T, error) {
	if q.closed {
		return nil, ErrQueueClosed
	}

	// Remember the first segment's size before remove so we can track the
	// growth caused by the 4-byte deletion marker it appends.
	firstOldSize := sizeOfFile(q.firstSegment.filePath())

	// Remove the first object from the first segment
	obj, err := q.firstSegment.remove()
	if errors.Is(err, errEmptySegment) {
		return nil, ErrEmpty
	}
	if err != nil {
		return nil, errors.Wrap(err, "error removing item from the first segment")
	}

	// remove() appended a 4-byte deletion marker and grew the first segment
	// without deleting it. Add that growth to the cached disk total.
	q.cachedDiskBytes += sizeOfFile(q.firstSegment.filePath()) - firstOldSize

	// If this segment is empty and we've reached the max for this segment
	// then delete the file and open the next one.
	if q.firstSegment.size() == 0 &&
		q.firstSegment.sizeOnDisk() >= q.config.ItemsPerSegment {

		// Size of the (now-marked) first segment file before deleting it.
		deletedSize := sizeOfFile(q.firstSegment.filePath())

		// Delete the segment file
		if err := q.firstSegment.delete(); err != nil {
			return obj, errors.Wrap(err, "error deleting queue segment "+q.firstSegment.filePath()+". Queue is in inconsistent state")
		}
		// Subtract the deleted file's bytes from the cached total.
		q.cachedDiskBytes -= deletedSize

		// We have only one segment and it's now empty so destroy it and
		// create a new one.
		if q.firstSegment.number == q.lastSegment.number {

			// Create the next segment
			seg, err := newQueueSegment[T](q.fullPath, q.firstSegment.number+1, q.turbo)
			if err != nil {
				return obj, errors.Wrap(err, "error creating new segment. Queue is in inconsistent state")
			}
			q.firstSegment = seg
			q.lastSegment = seg

			// Add the new replacement segment's bytes to the cached total.
			q.cachedDiskBytes += sizeOfFile(seg.filePath())

		} else {

			if q.firstSegment.number+1 == q.lastSegment.number {
				// We have 2 segments, moving down to 1 shared segment
				q.firstSegment = q.lastSegment
			} else {

				// Open the next segment
				seg, err := openQueueSegment[T](q.fullPath, q.firstSegment.number+1, q.turbo)
				if err != nil {
					return obj, errors.Wrap(err, "error creating new segment. Queue is in inconsistent state")
				}
				q.firstSegment = seg
			}

		}
	}

	return obj, nil
}

// Peek returns the first item in the queue without dequeueing it.
// When the queue is empty, nil and dque.ErrEmpty are returned.
// Do not use this method with multiple dequeueing threads or you may regret it.
func (q *DQue[T]) Peek() (*T, error) {
	// This is heavy-handed but it is safe
	q.mutex.Lock()
	defer q.mutex.Unlock()

	return q.peekLocked()
}

func (q *DQue[T]) peekLocked() (*T, error) {
	if q.closed {
		return nil, ErrQueueClosed
	}

	// Return the first object from the first segment
	obj, err := q.firstSegment.peek()
	if errors.Is(err, errEmptySegment) {
		return nil, ErrEmpty
	}
	if err != nil {
		// In reality this will (i.e. should not) never happen
		return nil, errors.Wrap(err, "error getting item from the first segment")
	}

	return obj, nil
}

// DequeueBlock behaves similar to Dequeue, but is a blocking call until an item is available.
func (q *DQue[T]) DequeueBlock() (*T, error) {
	q.mutex.Lock()
	for {
		obj, err := q.dequeueLocked()
		if errors.Is(err, ErrEmpty) {
			q.emptyCond.Wait()
			continue
		} else if err != nil {
			q.mutex.Unlock()
			return nil, err
		}
		q.mutex.Unlock()
		return obj, nil
	}
}

// PeekBlock behaves similar to Peek, but is a blocking call until an item is available.
func (q *DQue[T]) PeekBlock() (*T, error) {
	q.mutex.Lock()
	for {
		obj, err := q.peekLocked()
		if errors.Is(err, ErrEmpty) {
			q.emptyCond.Wait()
			continue
		} else if err != nil {
			q.mutex.Unlock()
			return nil, err
		}
		q.mutex.Unlock()
		return obj, nil
	}
}

// Size locks things up while calculating so you are guaranteed an accurate
// size... unless you have changed the itemsPerSegment value since the queue
// was last empty.  Then it could be wildly inaccurate.
func (q *DQue[T]) Size() int {
	// This is heavy-handed but it is safe
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if q.closed {
		return 0
	}

	return q.sizeUnsafeLocked()
}

// sizeUnsafeLocked calculates the approximate size while holding q.mutex.
func (q *DQue[T]) sizeUnsafeLocked() int {
	if q.firstSegment.number == q.lastSegment.number {
		return q.firstSegment.size()
	}
	numSegmentsBetween := q.lastSegment.number - q.firstSegment.number - 1
	return q.firstSegment.size() + (numSegmentsBetween * q.config.ItemsPerSegment) + q.lastSegment.size()
}

// DiskBytes returns an estimate of disk usage for the queue in bytes
// by summing the sizes of all segment files in the queue directory.
// Returns 0 when the queue is closed or on any filesystem error.
func (q *DQue[T]) DiskBytes() int64 {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if q.closed {
		return 0
	}
	return q.cachedDiskBytes
}

func (q *DQue[T]) diskBytesLocked() int64 {
	entries, err := osReadDir(q.fullPath)
	if err != nil {
		return 0
	}
	var total int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		total += info.Size()
	}
	return total
}

// sizeOfFile returns the size of the named file in bytes, or 0 if the file does
// not exist or cannot be stat'd. It is used to maintain the cached disk total
// as segments are added, grown, or deleted.
func sizeOfFile(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// SizeUnsafe returns the approximate number of items in the queue.  Use Size() if
// having the exact size is important to your use-case.
//
// The return value could be wildly inaccurate if the itemsPerSegment value has
// changed since the queue was last empty.
// Also, because the value is taken under lock, the size may change after
// returning from this method.
func (q *DQue[T]) SizeUnsafe() int {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	if q.closed {
		return 0
	}
	return q.sizeUnsafeLocked()
}

// SegmentNumbers returns the number of both the first and last segment.
// There is likely no use for this information other than testing.
func (q *DQue[T]) SegmentNumbers() (int, int) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if q.closed {
		return 0, 0
	}
	return q.firstSegment.number, q.lastSegment.number
}

// Turbo returns true if the turbo flag is on.  Having turbo on speeds things
// up significantly.
func (q *DQue[T]) Turbo() bool {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if q.closed {
		return false
	}
	return q.turbo
}

// TurboOn allows the filesystem to decide when to sync file changes to disk.
// Throughput is greatly increased by turning turbo on, however there is some
// risk of losing data if a power-loss occurs.
// If turbo is already on an error is returned
func (q *DQue[T]) TurboOn() error {
	// This is heavy-handed but it is safe
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if q.closed {
		return ErrQueueClosed
	}

	if q.turbo {
		return errors.New("DQue.TurboOn() is not valid when turbo is on")
	}
	q.turbo = true
	q.firstSegment.turboOn()
	q.lastSegment.turboOn()
	return nil
}

// TurboOff re-enables the "safety" mode that syncs every file change to disk as
// they happen.
// If turbo is already off an error is returned
func (q *DQue[T]) TurboOff() error {
	// This is heavy-handed but it is safe
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if q.closed {
		return ErrQueueClosed
	}

	if !q.turbo {
		return errors.New("DQue.TurboOff() is not valid when turbo is off")
	}
	if err := q.firstSegment.turboOff(); err != nil {
		return err
	}
	if err := q.lastSegment.turboOff(); err != nil {
		return err
	}
	q.turbo = false
	return nil
}

// TurboSync allows you to fsync changes to disk, but only if turbo is on.
// If turbo is off an error is returned
func (q *DQue[T]) TurboSync() error {
	// This is heavy-handed but it is safe
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if q.closed {
		return ErrQueueClosed
	}
	if !q.turbo {
		return errors.New("DQue.TurboSync() is inappropriate when turbo is off")
	}
	if err := q.firstSegment.turboSync(); err != nil {
		return errors.Wrap(err, "unable to sync changes to disk")
	}
	if err := q.lastSegment.turboSync(); err != nil {
		return errors.Wrap(err, "unable to sync changes to disk")
	}
	return nil
}

// load populates the queue from disk
func (q *DQue[T]) load() error {

	// Find all queue files
	files, err := osReadDir(q.fullPath)
	if err != nil {
		return errors.Wrap(err, "unable to read files in "+q.fullPath)
	}

	// Find the smallest and the largest file numbers
	minNum := math.MaxInt32
	maxNum := 0
	for _, f := range files {
		if !f.IsDir() && filePattern.MatchString(f.Name()) {
			// Extract number out of the filename
			fileNumStr := filePattern.FindStringSubmatch(f.Name())[1]
			fileNum, err := strconv.Atoi(fileNumStr)
			if err != nil {
				continue
			}
			if fileNum > maxNum {
				maxNum = fileNum
			}
			if fileNum < minNum {
				minNum = fileNum
			}
		}
	}

	// If files were found, set q.firstSegment and q.lastSegment
	if maxNum > 0 {

		// We found files. Skip any segments that are empty and complete.
		for minNum <= maxNum {
			seg, err := openQueueSegment[T](q.fullPath, minNum, q.turbo)
			if err != nil {
				return errors.Wrap(err, "unable to create queue segment in "+q.fullPath)
			}
			// Make sure the first segment is not empty or it's not complete (i.e. is current)
			if seg.size() > 0 || seg.sizeOnDisk() < q.config.ItemsPerSegment {
				q.firstSegment = seg
				break
			}
			// Delete the segment as it's empty and complete
			if err := seg.delete(); err != nil {
				return errors.Wrap(err, "unable to delete empty segment in "+q.fullPath)
			}
			// Try the next one
			minNum++
		}
	}

	switch {
	case q.firstSegment == nil:
		// We found no usable segments so build a new queue starting with segment 1
		seg, err := newQueueSegment[T](q.fullPath, 1, q.turbo)
		if err != nil {
			return errors.Wrap(err, "unable to create queue segment in "+q.fullPath)
		}

		// The first and last are the same instance (in this case)
		q.firstSegment = seg
		q.lastSegment = seg
	case minNum == maxNum:
		// We have only one segment so the
		// first and last are the same instance (in this case)
		q.lastSegment = q.firstSegment
	default:
		// We have multiple segments
		seg, err := openQueueSegment[T](q.fullPath, maxNum, q.turbo)
		if err != nil {
			return errors.Wrap(err, "unable to create segment for "+q.fullPath)
		}
		q.lastSegment = seg
	}

	return nil
}

func (q *DQue[T]) lock() error {
	l := filepath.Join(q.DirPath, q.Name, lockFile)
	fileLock := flock.New(l)

	acquired := false
	defer func() {
		if !acquired {
			_ = fileLock.Close()
		}
	}()

	locked, err := fileLock.TryLock()
	if err != nil {
		return err
	}
	if !locked {
		return errors.New("failed to acquire flock")
	}

	acquired = true
	q.fileLock = fileLock
	return nil
}

func (q *DQue[T]) initQueue(fullPath string, itemsPerSegment int) error {
	q.fullPath = fullPath
	q.config.ItemsPerSegment = itemsPerSegment
	q.emptyCond = sync.NewCond(&q.mutex)
	if err := q.lock(); err != nil {
		return err
	}
	if err := q.load(); err != nil {
		// Close any segments that were opened before the failure.
		if q.firstSegment != nil {
			_ = q.firstSegment.close()
		}
		if q.lastSegment != nil && q.lastSegment != q.firstSegment {
			_ = q.lastSegment.close()
		}
		if releaseErr := flockClose(q.fileLock); releaseErr != nil {
			return errors.Join(err, releaseErr)
		}
		return err
	}
	// Seed the cached disk total. A seed-time ReadDir error leaves the cache at
	// 0 but does NOT fail initQueue: load already completed successfully, so the
	// queue is usable. Subsequent DiskBytes() returns the cached (0) value.
	q.cachedDiskBytes = q.diskBytesLocked()
	return nil
}
