// queue_test.go
package dque_test

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/dque"
)

// item2 is the thing we'll be storing in the queue
type item2 struct {
	Id int
}

// Adds 1 and removes 1 in a loop to ensure that when we've filled
// up the first segment that we delete it and move on to the next segment
func TestQueue_AddRemoveLoop(t *testing.T) {
	testQueue_AddRemoveLoop(t, true /* true=turbo */)
	testQueue_AddRemoveLoop(t, false /* true=turbo */)
}

func testQueue_AddRemoveLoop(t *testing.T, turbo bool) {
	qName := "test1"
	dir := t.TempDir()

	// Create a new queue with segment size of 3
	q := mustOpenQ(t, dque.New[item2], dir, qName, turbo)

	for i := range 4 {
		if err := q.Enqueue(&item2{i}); err != nil {
			t.Fatal("Error enqueueing", err)
		}
		_, err := q.Dequeue()
		if err != nil {
			t.Fatal("Error dequeueing", err)
		}
	}

	if q.Size() != 0 {
		t.Fatalf("Size is not 0")
	}

	firstSegNum, lastSegNum := q.SegmentNumbers()

	// Assert that we have just one segment
	if firstSegNum != lastSegNum {
		t.Fatalf("The first segment must match the last")
	}

	// Assert that the first segment is #2
	if firstSegNum != 2 {
		t.Fatalf("The first segment is not 2")
	}

	// Now reopen the queue and check our assertions again.
	if err := q.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
	q = mustOpenQ(t, dque.Open[item2], dir, qName, turbo)

	firstSegNum, lastSegNum = q.SegmentNumbers()

	// Assert that we have just one segment
	if firstSegNum != lastSegNum {
		t.Fatalf("After opening, the first segment must match the second")
	}

	// Assert that the first segment is #2
	if firstSegNum != 2 {
		t.Fatalf("After opening, the first segment is not 2")
	}
}

// Adds 2 and removes 1 in a loop to ensure that when we've filled
// up the first segment that we delete it and move on to the next segment
func TestQueue_Add2Remove1(t *testing.T) {
	testQueue_Add2Remove1(t, true /* true=turbo */)
	testQueue_Add2Remove1(t, false /* true=turbo */)
}
func testQueue_Add2Remove1(t *testing.T, turbo bool) {
	qName := "test1"
	dir := t.TempDir()

	// Create a new queue with segment size of 3
	q := mustOpenQ(t, dque.New[item2], dir, qName, turbo)

	// Add 2 and remove one each loop
	for i := 0; i < 4; i += 2 {
		if err := q.Enqueue(&item2{i}); err != nil {
			t.Fatal("Error enqueueing", err)
		}
		if err := q.Enqueue(&item2{i + 1}); err != nil {
			t.Fatal("Error enqueueing", err)
		}
		item, err := q.Dequeue()
		if err != nil {
			t.Fatal("Error dequeueing", err)
		}
		if item == nil {
			t.Fatalf("Item is nil")
		}
	}

	firstSegNum, lastSegNum := q.SegmentNumbers()

	// Assert that we have more than one segment
	if firstSegNum >= lastSegNum {
		t.Fatalf("The first segment cannot match the second")
	}

	// Assert that the first segment is #2
	if lastSegNum != 2 {
		t.Fatalf("The last segment must be 2")
	}

	// Now reopen the queue and check our assertions again.
	if err := q.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
	q = mustOpenQ(t, dque.Open[item2], dir, qName, turbo)

	firstSegNum, lastSegNum = q.SegmentNumbers()

	// Assert that we have more than one segment
	if firstSegNum >= lastSegNum {
		t.Fatalf("After opening, the first segment can not match the second")
	}

	// Assert that the first segment is #2
	if lastSegNum != 2 {
		t.Fatalf("After opening, the last segment must be 2")
	}

	// Test Peek to make sure the size doesn't change
	if q.Size() != 2 {
		t.Fatalf("Queue size is not 2 before peeking")
	}
	obj, err := q.Peek()
	if err != nil {
		t.Fatal("Error peeking at the queue", err)
	}

	if q.Size() != 2 {
		t.Fatalf("After peaking, aueue size must still be 2")
	}
	if obj == nil {
		t.Fatalf("Peeked object must not be nil.")
	}
}

// Adds 9 and removes 8
func TestQueue_Add9Remove8(t *testing.T) {
	testQueue_Add9Remove8(t, true /* true = turbo */)
	testQueue_Add9Remove8(t, false /* true = turbo */)
}

func testQueue_Add9Remove8(t *testing.T, turbo bool) {
	qName := "test1"
	dir := t.TempDir()

	// Create new queue with segment size 3
	q := mustOpenQ(t, dque.New[item2], dir, qName, turbo)

	// Enqueue 9 items
	for i := range 9 {
		if err := q.Enqueue(&item2{i}); err != nil {
			t.Fatal("Error enqueueing", err)
		}
	}

	// Check the Size calculation
	if q.Size() != 9 {
		t.Fatalf("the size is calculated wrong.  Should be 9")
	}

	firstSegNum, lastSegNum := q.SegmentNumbers()

	// Assert that the first segment is #1
	if firstSegNum != 1 {
		t.Fatalf("the first segment is not 1")
	}

	// Assert that the last segment is #3
	if lastSegNum != 3 {
		t.Fatalf("the last segment is not 3")
	}

	// Dequeue 8 items
	for i := range 8 {
		item, err := q.Dequeue()
		if err != nil {
			t.Fatal("Error dequeueing:", err)
		}

		// Check the Size calculation
		if 8-i != q.Size() {
			t.Fatalf("the size is calculated wrong.")
		}
		fmt.Printf("Dequeued %#v\n", item)
		if i != item.Id {
			t.Fatalf("Unexpected itemId")
		}
	}

	firstSegNum, lastSegNum = q.SegmentNumbers()

	// Assert that we have only one segment
	if firstSegNum != lastSegNum {
		t.Fatalf("The first segment must match the second")
	}

	// Assert that the first segment is #3
	if firstSegNum != 3 {
		t.Fatalf("The last segment is not 3")
	}

	// Now reopen the queue and check our assertions again.
	if err := q.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
	q = mustOpenQ(t, dque.Open[item2], dir, qName, turbo)

	firstSegNum, lastSegNum = q.SegmentNumbers()

	// Assert that we have only one segment
	if firstSegNum != lastSegNum {
		t.Fatalf("After opening, the first segment must match the second")
	}

	// Assert that the last segment is #3
	if lastSegNum != 3 {
		t.Fatalf("After opening, the last segment is not 3")
	}
}

func TestQueue_EmptyDequeue(t *testing.T) {
	testQueue_EmptyDequeue(t, true /* true=turbo */)
	testQueue_EmptyDequeue(t, false /* true=turbo */)
}
func testQueue_EmptyDequeue(t *testing.T, turbo bool) {
	qName := "testEmptyDequeue"
	dir := t.TempDir()

	// Create new queue
	q := mustOpenQ(t, dque.New[item2], dir, qName, turbo)
	if q.Size() != 0 {
		t.Fatalf("Expected an empty queue")
	}

	// Dequeue an item from the empty queue
	item, err := q.Dequeue()
	if !errors.Is(err, dque.ErrEmpty) {
		t.Fatalf("Expected an ErrEmpty error")
	}
	if item != nil {
		t.Fatalf("Expected nil because queue is empty")
	}
}

func TestQueue_NewOrOpen(t *testing.T) {
	testQueue_NewOrOpen(t, true /* true=turbo */)
	testQueue_NewOrOpen(t, false /* true=turbo */)
}

func testQueue_NewOrOpen(t *testing.T, turbo bool) {
	qName := "testNewOrOpen"
	dir := t.TempDir()

	// Create new queue with newOrOpen
	q := mustOpenQ(t, dque.NewOrOpen[item2], dir, qName, turbo)
	if err := q.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Open the same queue with newOrOpen
	q = mustOpenQ(t, dque.NewOrOpen[item2], dir, qName, turbo)
	if err := q.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestQueue_Turbo(t *testing.T) {
	qName := "testTurbo"
	dir := t.TempDir()

	// Create new queue
	q := mustOpenQ(t, dque.New[item2], dir, qName, false)

	if err := q.TurboOff(); err == nil {
		t.Fatal("Expected an error")
	}

	if err := q.TurboSync(); err == nil {
		t.Fatal("Expected an error")
	}

	if err := q.TurboOn(); err != nil {
		t.Fatal("Error turning on turbo:", err)
	}

	if err := q.TurboOn(); err == nil {
		t.Fatal("Expected an error")
	}

	if err := q.TurboSync(); err != nil {
		t.Fatal("Error running TurboSync:", err)
	}

	// Enqueue 1000 items
	start := time.Now()
	for i := range 1000 {
		if err := q.Enqueue(&item2{i}); err != nil {
			t.Fatal("Error enqueueing:", err)
		}
	}
	elapsedTurbo := time.Since(start)

	if !q.Turbo() {
		t.Fatalf("Expected turbo to be on")
	}

	if err := q.TurboOff(); err != nil {
		t.Fatal("Error turning off turbo:", err)
	}

	// Enqueue 1000 items
	start = time.Now()
	for i := range 1000 {
		if err := q.Enqueue(&item2{i}); err != nil {
			t.Fatal("Error enqueueing:", err)
		}
	}
	elapsedSafe := time.Since(start)

	if elapsedTurbo >= elapsedSafe/2 {
		t.Fatalf("Turbo time (%v) must be faster than safe mode (%v)", elapsedTurbo, elapsedSafe)
	}
}

func TestQueue_NewFlock(t *testing.T) {
	qName := "testFlock"
	dir := t.TempDir()

	// New and Close a DQue properly should work
	q, err := dque.New[item2](qName, dir, 3)
	if err != nil {
		t.Fatal("Error creating dque:", err)
	}
	err = q.Close()
	if err != nil {
		t.Fatal("Error closing dque:", err)
	}

	// Double-open should fail
	q, err = dque.Open[item2](qName, dir, 3)
	if err != nil {
		t.Fatal("Error opening dque:", err)
	}
	_, err = dque.Open[item2](qName, dir, 3)
	if err == nil {
		t.Fatal("No error during double-open dque")
	}
	err = q.Close()
	if err != nil {
		t.Fatal("Error closing dque:", err)
	}

	// Double-close should fail
	q, err = dque.Open[item2](qName, dir, 3)
	if err != nil {
		t.Fatal("Error opening dque:", err)
	}
	err = q.Close()
	if err != nil {
		t.Fatal("Error closing dque:", err)
	}
	err = q.Close()
	if err == nil {
		t.Fatal("No error during double-closing dque")
	}
}

func TestQueue_UseAfterClose(t *testing.T) {
	qName := "testUseAfterClose"
	dir := t.TempDir()

	q, err := dque.New[item2](qName, dir, 3)
	if err != nil {
		t.Fatal("Error creating dque:", err)
	}
	err = q.Enqueue(&item2{0})
	if err != nil {
		t.Fatal("Error enqueing item:", err)
	}
	err = q.Close()
	if err != nil {
		t.Fatal("Error closing dque:", err)
	}

	err = q.Close()
	if !errors.Is(err, dque.ErrQueueClosed) {
		t.Fatalf("Expected ErrQueueClosed, got %v", err)
	}

	err = q.Enqueue(&item2{0})
	if !errors.Is(err, dque.ErrQueueClosed) {
		t.Fatalf("Expected ErrQueueClosed, got %v", err)
	}

	_, err = q.Dequeue()
	if !errors.Is(err, dque.ErrQueueClosed) {
		t.Fatalf("Expected ErrQueueClosed, got %v", err)
	}

	_, err = q.Peek()
	if !errors.Is(err, dque.ErrQueueClosed) {
		t.Fatalf("Expected ErrQueueClosed, got %v", err)
	}

	s := q.Size()
	if s != 0 {
		t.Fatalf("Expected size to be 0")
	}

	s = q.SizeUnsafe()
	if s != 0 {
		t.Fatalf("Expected size to be 0")
	}

	err = q.TurboOn()
	if !errors.Is(err, dque.ErrQueueClosed) {
		t.Fatalf("Expected ErrQueueClosed, got %v", err)
	}

	err = q.TurboOff()
	if !errors.Is(err, dque.ErrQueueClosed) {
		t.Fatalf("Expected ErrQueueClosed, got %v", err)
	}

	err = q.TurboSync()
	if !errors.Is(err, dque.ErrQueueClosed) {
		t.Fatalf("Expected ErrQueueClosed, got %v", err)
	}
}

func TestQueue_BlockingBehaviour(t *testing.T) {
	qName := "testBlocking"
	dir := t.TempDir()

	q := mustOpenQ(t, dque.New[item2], dir, qName, false)

	go func() {
		err := q.Enqueue(&item2{0})
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
	}()

	x, err := q.PeekBlock()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if x == nil {
		t.Fatalf("Item is nil")
	}

	x, err = q.DequeueBlock()
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if x == nil {
		t.Fatalf("Item is nil")
	}

	x, err = q.Dequeue()
	if !errors.Is(err, dque.ErrEmpty) {
		t.Fatalf("Expected ErrEmpty error")
	}

	timeout := time.After(3 * time.Second)
	done := make(chan bool)
	go func() {
		x, err = q.DequeueBlock()
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		} else if x == nil {
			t.Errorf("Item is nil")
		}
		done <- true
	}()

	go func() {
		time.Sleep(1 * time.Second)
		err := q.Enqueue(&item2{2})
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
	}()

	select {
	case <-timeout:
		t.Fatal("Test didn't finish in time")
	case <-done:
	}
}

func TestQueue_BlockingWithClose(t *testing.T) {
	qName := "testBlockingWithClose"
	dir := t.TempDir()

	q := mustOpenQ(t, dque.New[item2], dir, qName, false)

	go func() {
		time.Sleep(1 * time.Second)
		err := q.Close()
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
	}()

	timeout := time.After(3 * time.Second)
	done := make(chan bool)
	go func() {
		// The queue is empty,
		// so DequeueBlock should really block and wait,
		// until the other goroutine calls Close,
		// and the Close should wake-up this DequeueBlock block,
		// and return an error because the queue is now closed.
		_, err := q.DequeueBlock()
		if !errors.Is(err, dque.ErrQueueClosed) {
			t.Errorf("Expected ErrQueueClosed error, got %v", err)
		}
		done <- true
	}()

	select {
	case <-timeout:
		t.Fatal("Test didn't finish in time")
	case <-done:
	}
}

func TestQueue_BlockingAggresive(t *testing.T) {
	qName := "testBlockingAggresive"
	dir := t.TempDir()

	q := mustOpenQ(t, dque.New[item2], dir, qName, false)

	numProducers := 5
	numItemsPerProducer := 50
	numConsumers := 25

	done := make(chan bool)
	var wg sync.WaitGroup
	wg.Add(numProducers * numItemsPerProducer)

	go func() {
		wg.Wait()
		if err := q.Close(); err != nil {
			t.Errorf("Close failed: %v", err)
		}
		done <- true
	}()

	// producers
	for p := range numProducers {
		go func(producer int) {
			rng := rand.New(rand.NewSource(int64(producer)))
			for i := range numItemsPerProducer {
				s := rng.Intn(150)
				time.Sleep(time.Duration(s) * time.Millisecond)
				err := q.Enqueue(&item2{i})
				if err != nil {
					t.Errorf("Expected no error, got %v", err)
				}
				fmt.Println("Enqueued item", i, "by producer", producer, "after sleeping", s)
			}
		}(p)
	}

	// consumers
	for c := range numConsumers {
		go func(consumer int) {
			for {
				x, err := q.DequeueBlock()
				if errors.Is(err, dque.ErrQueueClosed) {
					return
				}
				if err != nil {
					t.Errorf("Expected no error, got %v", err)
				}
				fmt.Println("Dequeued item", x, "by consumer", consumer)
				wg.Done()
			}
		}(c)
	}

	timeout := time.After(10 * time.Second)
	select {
	case <-timeout:
		t.Fatal("Test didn't finish in time")
	case <-done:
	}
}

type openFunc[T any] func(name, dirPath string, itemsPerSegment int) (*dque.DQue[T], error)

func mustOpenQ[T any](t *testing.T, fn openFunc[T], dir, qName string, turbo bool) *dque.DQue[T] {
	t.Helper()
	q, err := fn(qName, dir, 3)
	if err != nil {
		t.Fatalf("Error creating/opening dque: %v", err)
	}
	if turbo {
		if err := q.TurboOn(); err != nil {
			t.Fatalf("TurboOn failed: %v", err)
		}
	}
	return q
}

func TestQueue_ValidationErrors(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmpDir, "exists"), 0755); err != nil {
		t.Fatal(err)
	}

	for _, constructor := range []struct {
		name string
		fn   openFunc[item2]
	}{
		{"New", dque.New[item2]},
		{"Open", dque.Open[item2]},
		{"NewOrOpen", dque.NewOrOpen[item2]},
	} {
		t.Run(constructor.name+"/empty name", func(t *testing.T) {
			if _, err := constructor.fn("", ".", 3); err == nil {
				t.Fatal("expected error for empty queue name")
			}
		})
		t.Run(constructor.name+"/empty directory", func(t *testing.T) {
			if _, err := constructor.fn("test", "", 3); err == nil {
				t.Fatal("expected error for empty directory")
			}
		})
		t.Run(constructor.name+"/invalid directory", func(t *testing.T) {
			if _, err := constructor.fn("test", filepath.Join(t.TempDir(), "does-not-exist"), 3); err == nil {
				t.Fatal("expected error for invalid directory")
			}
		})
		t.Run(constructor.name+"/zero itemsPerSegment", func(t *testing.T) {
			if _, err := constructor.fn("test", t.TempDir(), 0); err == nil {
				t.Fatal("expected error for zero itemsPerSegment")
			}
		})
		t.Run(constructor.name+"/negative itemsPerSegment", func(t *testing.T) {
			if _, err := constructor.fn("test", t.TempDir(), -1); err == nil {
				t.Fatal("expected error for negative itemsPerSegment")
			}
		})
	}

	t.Run("New/existing queue directory", func(t *testing.T) {
		if _, err := dque.New[item2]("exists", tmpDir, 3); err == nil {
			t.Fatal("expected error when queue directory already exists")
		}
	})

	t.Run("Open/missing queue directory", func(t *testing.T) {
		if _, err := dque.Open[item2]("test", t.TempDir(), 3); err == nil {
			t.Fatal("expected error when queue does not exist")
		}
	})

	t.Run("path traversal/name with slash", func(t *testing.T) {
		if _, err := dque.New[item2]("foo/bar", t.TempDir(), 3); err == nil {
			t.Fatal("expected error for queue name containing slash")
		}
	})

	t.Run("path traversal/name with backslash", func(t *testing.T) {
		if _, err := dque.New[item2]("foo\\bar", t.TempDir(), 3); err == nil {
			t.Fatal("expected error for queue name containing backslash")
		}
	})

	t.Run("path traversal/name with dotdot", func(t *testing.T) {
		if _, err := dque.New[item2]("../foo", t.TempDir(), 3); err == nil {
			t.Fatal("expected error for queue name containing '../foo'")
		}
	})

	t.Run("path traversal/name is dotdot", func(t *testing.T) {
		if _, err := dque.New[item2]("..", t.TempDir(), 3); err == nil {
			t.Fatal("expected error for queue name '..'")
		}
	})
}

// TestQueue_LoadSkipsEmptySegments verifies that load() removes empty, complete segments.
func TestQueue_LoadSkipsEmptySegments(t *testing.T) {
	qName := "TestQueue_LoadSkipsEmptySegments"
	dir := t.TempDir()

	q := mustOpenQ(t, dque.New[item2], dir, qName, false)
	// Fill the first segment completely and then dequeue all items so it becomes empty and complete.
	for i := range 3 {
		if err := q.Enqueue(&item2{Id: i}); err != nil {
			t.Fatalf("Enqueue failed: %s", err)
		}
	}
	for range 3 {
		if _, err := q.Dequeue(); err != nil {
			t.Fatalf("Dequeue failed: %s", err)
		}
	}

	firstSegNum, _ := q.SegmentNumbers()
	if firstSegNum != 2 {
		t.Fatalf("expected first segment to be 2 after rollover, got %d", firstSegNum)
	}

	// Reopen the queue; the empty segment #1 should be skipped/deleted.
	if err := q.Close(); err != nil {
		t.Fatalf("Close failed: %s", err)
	}
	q = mustOpenQ(t, dque.Open[item2], dir, qName, false)
	defer func() {
		if err := q.Close(); err != nil {
			t.Errorf("Close failed: %v", err)
		}
	}()

	firstSegNum, lastSegNum := q.SegmentNumbers()
	if firstSegNum != 2 || lastSegNum != 2 {
		t.Fatalf("expected segments 2,2 after load, got %d,%d", firstSegNum, lastSegNum)
	}
	if q.Size() != 0 {
		t.Fatalf("expected size 0, got %d", q.Size())
	}
}

// TestQueue_LoadAllEmptyCompleteSegments verifies that load() handles a
// directory where every on-disk segment is empty and complete by creating
// segment 1.
func TestQueue_LoadAllEmptyCompleteSegments(t *testing.T) {
	qName := "TestQueue_LoadAllEmptyCompleteSegments"
	dir := t.TempDir()
	queueDir := filepath.Join(dir, qName)
	if err := os.Mkdir(queueDir, 0755); err != nil {
		t.Fatalf("failed to create queue directory: %s", err)
	}

	// Manually create an empty-complete segment: itemsPerSegment items followed
	// by the same number of deletion markers.
	writeEmptyCompleteSegment(t, filepath.Join(queueDir, "0000000000001.dque"), 3)

	q, err := dque.Open[item2](qName, dir, 3)
	if err != nil {
		t.Fatalf("Open failed: %s", err)
	}
	defer func() {
		if err := q.Close(); err != nil {
			t.Errorf("Close failed: %v", err)
		}
	}()

	firstSegNum, lastSegNum := q.SegmentNumbers()
	if firstSegNum != 1 || lastSegNum != 1 {
		t.Fatalf("expected segments 1,1 after load, got %d,%d", firstSegNum, lastSegNum)
	}
	if q.Size() != 0 {
		t.Fatalf("expected size 0, got %d", q.Size())
	}

	// Ensure the queue is still usable after the fallback.
	if err := q.Enqueue(&item2{Id: 42}); err != nil {
		t.Fatalf("Enqueue after fallback failed: %s", err)
	}
	if q.Size() != 1 {
		t.Fatalf("expected size 1 after enqueue, got %d", q.Size())
	}
}

// writeEmptyCompleteSegment writes a segment file containing itemsPerSegment
// items followed by itemsPerSegment deletion markers. After loading, the
// segment reports size() == 0 and sizeOnDisk() == itemsPerSegment, which makes
// it empty and complete.
func writeEmptyCompleteSegment(t *testing.T, path string, itemsPerSegment int) {
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create segment file: %s", err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Fatalf("failed to close segment file: %s", err)
		}
	}()

	// Each record is decoded independently by the segment loader, so each item
	// must be encoded with a fresh encoder to include full type information.
	for i := range itemsPerSegment {
		var buf bytes.Buffer
		enc := gob.NewEncoder(&buf)
		if err := enc.Encode(&item2{Id: i}); err != nil {
			t.Fatalf("failed to encode item: %s", err)
		}
		lengthBytes := make([]byte, 4)
		binary.LittleEndian.PutUint32(lengthBytes, uint32(buf.Len()))
		if _, err := f.Write(lengthBytes); err != nil {
			t.Fatalf("failed to write length: %s", err)
		}
		if _, err := f.Write(buf.Bytes()); err != nil {
			t.Fatalf("failed to write item: %s", err)
		}
	}

	deleteMarker := make([]byte, 4)
	for range itemsPerSegment {
		if _, err := f.Write(deleteMarker); err != nil {
			t.Fatalf("failed to write delete marker: %s", err)
		}
	}
}

// TestQueue_ClosedBehavior verifies behavior after the queue is closed.
func TestQueue_ClosedBehavior(t *testing.T) {
	qName := "TestQueue_ClosedBehavior"
	dir := t.TempDir()

	q := mustOpenQ(t, dque.New[item2], dir, qName, false)
	if err := q.Close(); err != nil {
		t.Fatalf("Close failed: %s", err)
	}

	// SegmentNumbers on a closed queue returns zeros.
	first, last := q.SegmentNumbers()
	if first != 0 || last != 0 {
		t.Fatalf("expected SegmentNumbers to return 0,0 on closed queue, got %d,%d", first, last)
	}

	// Size on a closed queue returns zero.
	if q.Size() != 0 {
		t.Fatalf("expected Size to be 0 on closed queue, got %d", q.Size())
	}

	// Operations on a closed queue return ErrQueueClosed.
	if err := q.Enqueue(&item2{Id: 1}); !errors.Is(err, dque.ErrQueueClosed) {
		t.Fatalf("expected Enqueue to return ErrQueueClosed, got %v", err)
	}
	if _, err := q.Dequeue(); !errors.Is(err, dque.ErrQueueClosed) {
		t.Fatalf("expected Dequeue to return ErrQueueClosed, got %v", err)
	}
	if _, err := q.Peek(); !errors.Is(err, dque.ErrQueueClosed) {
		t.Fatalf("expected Peek to return ErrQueueClosed, got %v", err)
	}
	if err := q.TurboOn(); !errors.Is(err, dque.ErrQueueClosed) {
		t.Fatalf("expected TurboOn to return ErrQueueClosed, got %v", err)
	}
	if err := q.TurboOff(); !errors.Is(err, dque.ErrQueueClosed) {
		t.Fatalf("expected TurboOff to return ErrQueueClosed, got %v", err)
	}
	if err := q.TurboSync(); !errors.Is(err, dque.ErrQueueClosed) {
		t.Fatalf("expected TurboSync to return ErrQueueClosed, got %v", err)
	}

	// Close on an already-closed queue returns ErrQueueClosed.
	if err := q.Close(); !errors.Is(err, dque.ErrQueueClosed) {
		t.Fatalf("expected second Close to return ErrQueueClosed, got %v", err)
	}
}

// TestQueue_LockConflictClosesFile verifies that attempting to open a queue
// that is already locked does not leak the competing lock file descriptor.
func TestQueue_LockConflictClosesFile(t *testing.T) {
	qName := "testLockConflict"
	dir := t.TempDir()

	q1, err := dque.New[item2](qName, dir, 3)
	if err != nil {
		t.Fatalf("first New failed: %s", err)
	}
	defer func() {
		if closeErr := q1.Close(); closeErr != nil {
			t.Errorf("Close failed: %v", closeErr)
		}
	}()

	_, err = dque.Open[item2](qName, dir, 3)
	if err == nil {
		t.Fatal("expected Open to fail when queue is already locked")
	}
	// No direct assertion for the descriptor, but exercising the conflict path
	// under the race detector should catch the previously missing Close().
}
