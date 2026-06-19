// generic_test.go
//
// Tests verifying that the DQue API is type-safe at compile time via generics.
// Constructors New, Open, and NewOrOpen accept a type parameter instead of a
// builder function. Enqueue accepts the item type directly. Dequeue, Peek,
// DequeueBlock, and PeekBlock return the item type directly — no interface{}
// return values and no manual type assertions needed.
package dque_test

import (
	"errors"
	"testing"

	"github.com/lbe/sfpg-go/internal/dque"
)

// genericItem is the type used in the generic queue type-safety test.
type genericItem struct {
	Name string
	Id   int
}

// TestQueue_TypeSafeAPI verifies that the queue API is fully type-safe at compile
// time. Constructors New, Open, and NewOrOpen accept a type parameter instead of
// a builder function. Enqueue accepts the item type directly. Dequeue, Peek,
// DequeueBlock, and PeekBlock return the item type directly — no interface{}
// return values and no manual type assertions anywhere in the test.
func TestQueue_TypeSafeAPI(t *testing.T) {
	qName := "testTypeSafe"
	dir := t.TempDir()

	// New[T] accepts a type parameter, no builder function.
	q, err := dque.New[genericItem](qName, dir, 3)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer func() {
		if closeErr := q.Close(); closeErr != nil {
			t.Errorf("Close failed: %v", closeErr)
		}
	}()

	// Enqueue accepts the concrete type directly, not interface{}.
	if err = q.Enqueue(&genericItem{Name: "first", Id: 1}); err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}
	if err = q.Enqueue(&genericItem{Name: "second", Id: 2}); err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	if q.Size() != 2 {
		t.Fatalf("expected size 2, got %d", q.Size())
	}

	// Dequeue returns *genericItem directly — no type assertion needed.
	item1, err := q.Dequeue()
	if err != nil {
		t.Fatalf("Dequeue failed: %v", err)
	}
	if item1.Name != "first" || item1.Id != 1 {
		t.Fatalf("expected {first 1}, got {%s %d}", item1.Name, item1.Id)
	}

	// Peek returns *genericItem directly — no type assertion needed.
	item2, err := q.Peek()
	if err != nil {
		t.Fatalf("Peek failed: %v", err)
	}
	if item2.Name != "second" || item2.Id != 2 {
		t.Fatalf("expected {second 2}, got {%s %d}", item2.Name, item2.Id)
	}

	// Size is unchanged after Peek.
	if q.Size() != 1 {
		t.Fatalf("expected size 1 after peek, got %d", q.Size())
	}

	// Drain the remaining "second" item first (FIFO order).
	item2b, err := q.Dequeue()
	if err != nil {
		t.Fatalf("Dequeue failed: %v", err)
	}
	if item2b.Name != "second" || item2b.Id != 2 {
		t.Fatalf("expected {second 2}, got {%s %d}", item2b.Name, item2b.Id)
	}

	// DequeueBlock returns *genericItem directly — no type assertion needed.
	// Queue is empty, so DequeueBlock blocks until the goroutine enqueues "third".
	go func() {
		if enqErr := q.Enqueue(&genericItem{Name: "third", Id: 3}); enqErr != nil {
			t.Errorf("Enqueue failed: %v", enqErr)
		}
	}()
	item3, err := q.DequeueBlock()
	if err != nil {
		t.Fatalf("DequeueBlock failed: %v", err)
	}
	if item3.Name != "third" || item3.Id != 3 {
		t.Fatalf("expected {third 3}, got {%s %d}", item3.Name, item3.Id)
	}

	// Dequeue on empty queue returns nil and ErrEmpty.
	item, err := q.Dequeue()
	if !errors.Is(err, dque.ErrEmpty) {
		t.Fatalf("expected ErrEmpty, got %v", err)
	}
	if item != nil {
		t.Fatalf("expected nil item on empty dequeue, got %+v", item)
	}

	// PeekBlock returns *genericItem directly — no type assertion needed.
	go func() {
		if enqErr := q.Enqueue(&genericItem{Name: "fourth", Id: 4}); enqErr != nil {
			t.Errorf("Enqueue failed: %v", enqErr)
		}
	}()
	item4, err := q.PeekBlock()
	if err != nil {
		t.Fatalf("PeekBlock failed: %v", err)
	}
	if item4.Name != "fourth" || item4.Id != 4 {
		t.Fatalf("expected {fourth 4}, got {%s %d}", item4.Name, item4.Id)
	}
}

// TestQueue_TypeSafeOpenAndNewOrOpen verifies that Open[T] and NewOrOpen[T]
// also work without builder functions.
func TestQueue_TypeSafeOpenAndNewOrOpen(t *testing.T) {
	qName := "testTypeSafeOpen"
	dir := t.TempDir()

	// Create with NewOrOpen[T].
	q, err := dque.NewOrOpen[genericItem](qName, dir, 3)
	if err != nil {
		t.Fatalf("NewOrOpen failed: %v", err)
	}
	if err = q.Enqueue(&genericItem{Name: "a", Id: 1}); err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}
	if err = q.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Open with Open[T] — no builder function needed.
	q, err = dque.Open[genericItem](qName, dir, 3)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer func() {
		if closeErr := q.Close(); closeErr != nil {
			t.Errorf("Close failed: %v", closeErr)
		}
	}()

	item, err := q.Dequeue()
	if err != nil {
		t.Fatalf("Dequeue failed: %v", err)
	}
	if item.Name != "a" || item.Id != 1 {
		t.Fatalf("expected {a 1}, got {%s %d}", item.Name, item.Id)
	}
}
