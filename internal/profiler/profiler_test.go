package profiler

import (
	"testing"
)

func TestStart_EmptyMode_ReturnsNoOpStop(t *testing.T) {
	stop, err := Start(Config{Mode: ""})
	if err != nil {
		t.Fatalf("Start(empty): %v", err)
	}
	if stop == nil {
		t.Fatal("stop func should be non-nil")
	}
	stop() // should not panic
	if Dir() != "" {
		t.Errorf("Dir() = %q, want empty when mode empty", Dir())
	}
}

func TestStart_InvalidMode_ReturnsError(t *testing.T) {
	stop, err := Start(Config{Mode: "invalid"})
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if stop != nil {
		t.Fatal("stop should be nil on error")
	}
}

func TestStart_AllocsMode_StopSucceeds(t *testing.T) {
	stop, err := Start(Config{Mode: "allocs"})
	if err != nil {
		t.Fatalf("Start(allocs): %v", err)
	}
	defer stop()
	if Dir() == "" {
		t.Error("Dir() should be set while profiler running")
	}
	if Mode() != "allocs" {
		t.Errorf("Mode() = %q, want allocs", Mode())
	}
	stop() // stop; second call is no-op per pkg/profile
}

func TestStart_HeapMode_StopSucceeds(t *testing.T) {
	stop, err := Start(Config{Mode: "heap"})
	if err != nil {
		t.Fatalf("Start(heap): %v", err)
	}
	defer stop()
	if Mode() != "heap" {
		t.Errorf("Mode() = %q, want heap", Mode())
	}
	stop()
}
