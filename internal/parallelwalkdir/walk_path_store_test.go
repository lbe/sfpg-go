package parallelwalkdir

import (
	"sync"
	"testing"
)

func TestWalkPathStore_AppendAndReset(t *testing.T) {
	s := NewWalkPathStore()
	p1 := s.AppendPath([]byte("foo"), 3)
	p2 := s.AppendPath([]byte("barbaz"), 6)
	if string(p1) != "foo" || string(p2) != "barbaz" {
		t.Fatalf("paths %q %q", p1, p2)
	}
	if cap(s.arena) < s.used {
		t.Fatalf("cap %d < used %d", cap(s.arena), s.used)
	}
	s.Reset()
	if s.used != 0 {
		t.Fatalf("used after reset: %d", s.used)
	}
	p3 := s.AppendPath([]byte("x"), 1)
	if string(p3) != "x" {
		t.Fatalf("after reset: %q", p3)
	}
	if cap(s.arena) == 0 {
		t.Fatal("expected retained cap after reset")
	}
}

func TestWalkPathStore_Grow(t *testing.T) {
	s := NewWalkPathStore()
	const chunk = 1024
	buf := make([]byte, chunk)
	for i := range buf {
		buf[i] = 'a'
	}
	for range 200 {
		s.AppendPath(buf, chunk)
	}
	if cap(s.arena) < s.used {
		t.Fatalf("cap %d < used %d", cap(s.arena), s.used)
	}
}

func TestWalkPathStore_ConcurrentAppend(t *testing.T) {
	s := NewWalkPathStore()
	var wg sync.WaitGroup
	const workers = 8
	const perWorker = 500
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			buf := []byte("path/to/file.jpg")
			for range perWorker {
				p := s.AppendPath(buf, len(buf))
				if len(p) != len(buf) {
					t.Error("bad len")
				}
			}
		}()
	}
	wg.Wait()
	if s.used != workers*perWorker*len("path/to/file.jpg") {
		t.Fatalf("used %d", s.used)
	}
}
