package dque_test

//
// Example usage
// Run with: go test -v example_test.go
//

import (
	"fmt"
	"log"
	"os"

	"github.com/lbe/sfpg-go/internal/dque"
)

// Item is what we'll be storing in the queue.  It can be any struct
// as long as the fields you want stored are public.
type Item struct {
	Name string
	Id   int
}

// ExampleDQue shows how the queue works
func ExampleDQue() {
	qName := "item-queue"
	qDir := os.TempDir()
	segmentSize := 50

	// Create a new queue with segment size of 50
	q, err := dque.NewOrOpen[Item](qName, qDir, segmentSize)
	if err != nil {
		log.Fatal("Error creating new dque ", err)
	}

	// Add an item to the queue
	if err = q.Enqueue(&Item{"Joe", 1}); err != nil {
		log.Fatal("Error enqueueing item ", err)
	}
	log.Println("Size should be 1:", q.Size())

	// Properly close a queue
	if err = q.Close(); err != nil {
		log.Fatal("Error closing dque ", err)
	}

	// You can reconsitute the queue from disk at any time
	q, err = dque.Open[Item](qName, qDir, segmentSize)
	if err != nil {
		log.Fatal("Error opening existing dque ", err)
	}

	// Peek at the next item in the queue
	item, err := q.Peek()
	if err != nil {
		if err != dque.ErrEmpty {
			log.Fatal("Error peeking at item", err)
		}
	}
	log.Println("Peeked at:", item)

	// Dequeue the next item in the queue
	item, err = q.Dequeue()
	if err != nil && err != dque.ErrEmpty {
		log.Fatal("Error dequeuing item:", err)
	}
	log.Println("Dequeued an item:", item)
	log.Println("Size should be zero:", q.Size())

	go func() {
		_ = q.Enqueue(&Item{"Joe", 1})
	}()

	// Dequeue the next item in the queue and block until one is available
	item, err = q.DequeueBlock()
	if err != nil {
		log.Fatal("Error dequeuing item ", err)
	}

	doSomething(item)
	// Output: Dequeued: &{Joe 1}
}

func doSomething(item *Item) {
	fmt.Println("Dequeued:", item)
}
