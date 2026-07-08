package profiler

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/pkg/profile"
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

func TestStart_MkdirTempFails(t *testing.T) {
	origDir := profileDir
	origMode := profileModeStr
	t.Cleanup(func() {
		mu.Lock()
		profileDir = origDir
		profileModeStr = origMode
		mu.Unlock()
	})

	origMkdirTemp := osMkdirTemp
	t.Cleanup(func() { osMkdirTemp = origMkdirTemp })

	var gotDir, gotPattern string
	osMkdirTemp = func(dir, pattern string) (string, error) {
		gotDir = dir
		gotPattern = pattern
		return "", errors.New("mkdir denied")
	}

	origProfileStartFn := profileStartFn
	t.Cleanup(func() { profileStartFn = origProfileStartFn })
	profileStartFn = func(...func(*profile.Profile)) profilerHandle {
		panic("profile.Start should not be called when MkdirTemp fails")
	}

	stop, err := Start(Config{Mode: "cpu"})
	if err == nil {
		t.Fatal("expected error when MkdirTemp fails")
	}
	if stop != nil {
		t.Fatal("stop should be nil on error")
	}
	if gotDir != "" || gotPattern != "profile" {
		t.Errorf("osMkdirTemp called with (%q, %q), want (_, \"profile\")", gotDir, gotPattern)
	}
	if !strings.Contains(err.Error(), "failed to create temporary profile dir") {
		t.Fatalf("expected error message to contain \"failed to create temporary profile dir\", got: %v", err)
	}
	if !strings.Contains(err.Error(), "mkdir denied") {
		t.Fatalf("expected error message to contain \"mkdir denied\", got: %v", err)
	}
}

func TestStart_AllModes_ProfileStartCalled(t *testing.T) {
	tests := []struct {
		mode        string
		wantMode    string
		wantProfile func(*profile.Profile)
		wantOptsLen int
	}{
		{"CPU", "cpu", profile.CPUProfile, 4},
		{"Mem", "mem", profile.MemProfile, 4},
		{"Allocs", "allocs", profile.MemProfileAllocs, 5},
		{"Heap", "heap", profile.MemProfileHeap, 5},
		{"Mutex", "mutex", profile.MutexProfile, 4},
		{"Block", "block", profile.BlockProfile, 4},
		{"Trace", "trace", profile.TraceProfile, 4},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			origDir := profileDir
			origMode := profileModeStr
			t.Cleanup(func() {
				mu.Lock()
				profileDir = origDir
				profileModeStr = origMode
				mu.Unlock()
			})

			origMkdirTemp := osMkdirTemp
			t.Cleanup(func() { osMkdirTemp = origMkdirTemp })

			mockDir := t.TempDir()
			var gotDir, gotPattern string
			osMkdirTemp = func(dir, pattern string) (string, error) {
				gotDir = dir
				gotPattern = pattern
				return mockDir, nil
			}

			origProfileStartFn := profileStartFn
			t.Cleanup(func() { profileStartFn = origProfileStartFn })

			mockProfiler := &mockProfilerHandle{}
			var capturedOpts []func(*profile.Profile)
			var callCount int
			profileStartFn = func(opts ...func(*profile.Profile)) profilerHandle {
				callCount++
				capturedOpts = opts
				return mockProfiler
			}

			stop, err := Start(Config{Mode: tt.mode})
			if err != nil {
				t.Fatalf("Start(%q): %v", tt.mode, err)
			}
			if stop == nil {
				t.Fatal("stop should be non-nil on success")
			}

			if Dir() != mockDir {
				t.Errorf("Dir() = %q, want %q", Dir(), mockDir)
			}
			if Mode() != tt.wantMode {
				t.Errorf("Mode() = %q, want %q", Mode(), tt.wantMode)
			}
			if gotDir != "" || gotPattern != "profile" {
				t.Errorf("osMkdirTemp called with (%q, %q), want (_, \"profile\")", gotDir, gotPattern)
			}
			if callCount != 1 {
				t.Fatalf("profileStartFn called %d times, want 1", callCount)
			}
			if len(capturedOpts) != tt.wantOptsLen {
				t.Fatalf("captured opts length = %d, want %d", len(capturedOpts), tt.wantOptsLen)
			}
			if reflect.ValueOf(capturedOpts[0]).Pointer() != reflect.ValueOf(tt.wantProfile).Pointer() {
				t.Fatalf("first opt does not match expected profile function for mode %q", tt.wantMode)
			}

			stop()
			if mockProfiler.stopCount != 1 {
				t.Fatalf("mock profiler Stop() called %d times, want 1", mockProfiler.stopCount)
			}
		})
	}
}

type mockProfilerHandle struct {
	stopCount int
}

func (m *mockProfilerHandle) Stop() {
	m.stopCount++
}
