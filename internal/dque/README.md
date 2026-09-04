# dque - a fast embedded durable queue for Go

[![GoDoc](https://godoc.org/github.com/lbe/sfpg-go?status.svg)](https://godoc.org/github.com/lbe/sfpg-go)

dque is:

- persistent -- survives program restarts
- scalable -- not limited by your RAM, but by your disk space
- FIFO -- First In First Out
- embedded -- compiled into your Golang program
- synchronized -- safe for concurrent usage
- fast or safe, you choose -- turbo mode lets the OS decide when to write to disk
- has a liberal license -- allows any use, commercial or personal

I love tools that do one thing well. Hopefully this fits that category.

I am indebted to Gabor Cselle who, years ago, inspired me with an example of an [in-memory persistent queue written in Java](http://www.gaborcselle.com/open_source/java/persistent_queue.html). I was intrigued by the simplicity of his approach, which became the foundation of the "segment" part of this queue which holds the head and the tail of the queue in memory as well as storing the segment files in between.

### performance

There are two performance modes: safe and turbo

##### safe mode

- safe mode is the default
- forces an fsync to disk every time you enqueue or dequeue an item.
- while this is the safest way to use dque with little risk of data loss, it is also the slowest.

##### turbo mode

- can be enabled/disabled with a call to [DQue.TurboOn()](https://godoc.org/github.com/lbe/sfpg-go#DQue.TurboOn) or [DQue.TurboOff()](https://godoc.org/github.com/lbe/sfpg-go#DQue.TurboOff)
- lets the OS batch up your changes to disk, which makes it a lot faster.
- also allows you to flush changes to disk at opportune times. See [DQue.TurboSync()](https://godoc.org/github.com/lbe/sfpg-go#DQue.TurboSync)
- comes with a risk that a power failure could lose changes. By turning on Turbo mode you accept that risk.
- run the benchmark to see the difference on your hardware.
- there is a todo item to force flush changes to disk after a configurable amount of time to limit risk.

### implementation

- The queue is held in segments of a configurable size.
- The queue is protected against re-opening from other processes.
- Each in-memory segment corresponds with a file on disk. Think of the segment files as a bit like rolling log files. The oldest segment files are eventually deleted, not based on time, but whenever their items have all been dequeued.
- Segment files are only appended to until they fill up. At which point a new segment is created. They are never modified (other than being appended to and deleted when each of their items has been dequeued).
- If there is more than one segment, new items are enqueued to the last segment while dequeued items are taken from the first segment.
- Because the encoding/gob package is used to store the struct to disk:
  - Only structs can be stored in the queue.
  - Only one type of struct can be stored in each queue.
  - Only public fields in a struct will be stored.
  - A function is required that returns a pointer to a new struct of the type stored in the queue. This function is used when loading segments into memory from disk. **As of v2.1, this is no longer necessary — the queue uses Go generics and `new(T)` internally.**
- Queue segment implementation:
  - For nice visuals, see [Gabor Cselle's documentation here](http://www.gaborcselle.com/open_source/java/persistent_queue.html). Note that Gabor's implementation kept the entire queue in memory as well as disk. dque keeps only the head and tail segments in memory.
  - Enqueueing an item adds it both to the end of the last segment file and to the in-memory item slice for that segment.
  - When a segment reaches its maximum size a new segment is created.
  - Dequeueing an item removes it from the beginning of the in-memory slice and appends a 4-byte "delete" marker to the end of the segment file. This allows the item to be left in the file until the number of delete markers matches the number of items, at which point the entire file is deleted.
  - When a segment is reconstituted from disk, each "delete" marker found in the file causes a removal of the first element of the in-memory slice.
  - When each item in the segment has been dequeued, the segment file is deleted and the next segment is loaded into memory.

### example

See the [full example code here](https://raw.githubusercontent.com/lbe/sfpg-go/v2/example_test.go)

Or a shortened version here:

```golang
package dque_test

import (
    "log"

    "github.com/lbe/sfpg-go"
)

// Item is what we'll be storing in the queue.  It can be any struct
// as long as the fields you want stored are public.
type Item struct {
    Name string
    Id   int
}

func main() {
    ExampleDQue_main()
}

// ExampleQueue_main() show how the queue works
func ExampleDQue_main() {
    qName := "item-queue"
    qDir := "/tmp"
    segmentSize := 50

    // Create a new queue with segment size of 50
    q, err := dque.New[*Item](qName, qDir, segmentSize)
    ...

    // Add an item to the queue
    err := q.Enqueue(&Item{"Joe", 1})
    ...

    // Properly close a queue
    q.Close()

    // You can reconsitute the queue from disk at any time
    q, err = dque.Open[*Item](qName, qDir, segmentSize)
    ...

    // Peek at the next item in the queue
    item, err := q.Peek()
    if err != nil {
        if err != dque.ErrEmpty {
            log.Fatal("Error peeking at item ", err)
        }
    }

    // Dequeue the next item in the queue
    item, err = q.Dequeue()
    if err != nil {
        if err != dque.ErrEmpty {
            log.Fatal("Error dequeuing item ", err)
        }
    }

    // Dequeue the next item in the queue and block until one is available
    if item, err = q.DequeueBlock(); err != nil {
        log.Fatal("Error dequeuing item ", err)
    }

    doSomething(item)
}

func doSomething(item *Item) {
    log.Println("Dequeued", item)
}
```

### v2 migration notes

- Go **1.26.0** is now required.
- The public `Config` type is now unexported (`config`).
- `DQUE_EMPTY` has been replaced by the sentinel error `ErrEmpty`.
- The internal error helpers now capture stack traces. Wrapped errors include the
  stack trace in `Error()`, so avoid exact-string comparisons and use
  `errors.Is` / `errors.As` instead.
- The external dependencies `github.com/pkg/errors` and `github.com/gofrs/flock`
  have been removed and replaced with internal packages.

### v2.1 migration notes

- **Generics:** `DQue` is now `DQue[T any]`. Constructors `New`, `Open`, and
  `NewOrOpen` accept a type parameter instead of a builder function. Remove the
  `builder func() interface{}` argument from all constructor calls.
- **Type-safe API:** `Enqueue` accepts `T` directly. `Dequeue`, `Peek`,
  `DequeueBlock`, and `PeekBlock` return `(T, error)` instead of
  `(interface{}, error)`. Remove all manual type assertions on return values.

### contributors

- [Neil Isaac](https://github.com/neilisaac)
- [Thomas Kriechbaumer](https://github.com/Kriechi)

### todo? (feel free to submit pull requests)

- add option to enable turbo with a timeout that would ensure you would never lose more than n seconds of changes.
- add Lock() and Unlock() methods so you can peek at the first item and then conditionally dequeue it without worrying that another goroutine has grabbed it out from under you. The use case is when you don't want to actually remove it from the queue until you know you were able to successfully handle it.
- store the segment size in a config file inside the queue. Then it only needs to be specified on dque.New(...)

### alternative tools

- [CurlyQ](https://github.com/mcmathja/curlyq) is a bit heavier (requires Redis) but has more background processing features.
