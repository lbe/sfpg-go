// benchmark_test.go
//
// Benchmarks to see how long each operation takes on average.
//
// Example:   go test -bench=.
package dque_test

import (
	"testing"

	"github.com/lbe/sfpg-go/internal/dque"
)

// item3 is the thing we'll be storing in the queue
type item3 struct {
	Name     string
	Id       int
	SomeBool bool
}

func BenchmarkEnqueue_Safe(b *testing.B) {
	benchmarkEnqueue(b, false /* true=turbo */)
}
func BenchmarkEnqueue_Turbo(b *testing.B) {
	benchmarkEnqueue(b, true /* true=turbo */)
}

func benchmarkEnqueue(b *testing.B, turbo bool) {

	qName := "testBenchEnqueue"
	dir := b.TempDir()

	b.StopTimer()

	// Create the queue
	q, err := dque.New[item3](qName, dir, 100)
	if err != nil {
		b.Fatal("Error creating new dque:", err)
	}
	if turbo {
		if err := q.TurboOn(); err != nil {
			b.Fatal("TurboOn:", err)
		}
	}
	b.StartTimer()

	for n := 0; n < b.N; n++ {
		err := q.Enqueue(&item3{"Short Name", n, true})
		if err != nil {
			b.Fatal("Error enqueuing to dque:", err)
		}
	}
}

func BenchmarkDequeue_Safe(b *testing.B) {
	benchmarkDequeue(b, false /* true=turbo */)
}
func BenchmarkDequeue_Turbo(b *testing.B) {
	benchmarkDequeue(b, true /* true=turbo */)
}

func benchmarkDequeue(b *testing.B, turbo bool) {

	qName := "testBenchDequeue"
	dir := b.TempDir()

	b.StopTimer()

	// Create the queue
	q, err := dque.New[item3](qName, dir, 100)
	if err != nil {
		b.Fatal("Error creating new dque", err)
	}
	iterations := 5000
	if turbo {
		if err := q.TurboOn(); err != nil {
			b.Fatal("TurboOn:", err)
		}
		iterations *= 10
	}

	for i := 0; i < iterations; i++ {
		err := q.Enqueue(&item3{"Sorta, kind of, a Big Long Name", i, true})
		if err != nil {
			b.Fatal("Error enqueuing to dque:", err)
		}
	}
	b.StartTimer()

	for n := 0; n < b.N; n++ {
		_, err := q.Dequeue()
		if err != nil {
			b.Fatal("Error dequeuing from dque:", err)
		}
	}
}
