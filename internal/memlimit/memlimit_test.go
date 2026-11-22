package memlimit

import (
	"math"
	"os"
	"runtime/debug"
	"testing"
)

const gib = 1 << 30

func TestCompute(t *testing.T) {
	need := Need{Workers: 22, ExifToolServers: 4, RetainedBytes: 256 * mib}
	tests := []struct {
		name        string
		ram         uint64
		wantLimit   int64
		wantWorkers int
		wantSource  string
	}{
		{"fits", 16 * gib, 914 * mib, 22, "computed"},
		{"workers reduced", 1000 * mib, 641 * mib, 1, "workers_reduced"},
		{"capped", 300 * mib, 225 * mib, 1, "capped"},
		{"RAM unknown", 0, 914 * mib, 22, "computed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limit, workers, source := compute(tt.ram, need)
			if limit != tt.wantLimit || workers != tt.wantWorkers || source != tt.wantSource {
				t.Errorf("compute = %d, %d, %q; want %d, %d, %q",
					limit, workers, source, tt.wantLimit, tt.wantWorkers, tt.wantSource)
			}
		})
	}
}

// restoreRuntime puts the process memory limit and GC percent back after a
// test that changes them.
func restoreRuntime(t *testing.T) {
	t.Helper()
	limit := debug.SetMemoryLimit(-1)
	gc := debug.SetGCPercent(-1)
	debug.SetGCPercent(gc)
	t.Cleanup(func() {
		debug.SetMemoryLimit(limit)
		debug.SetGCPercent(gc)
	})
}

func TestApplyWithGOMEMLIMIT(t *testing.T) {
	restoreRuntime(t)
	t.Setenv("GOMEMLIMIT", "1GiB")
	before := debug.SetMemoryLimit(-1)
	debug.SetGCPercent(150)

	l := Apply(Need{Workers: 8, ExifToolServers: 2})
	if l.Workers != 8 {
		t.Errorf("Workers = %d, want 8", l.Workers)
	}
	if got := debug.SetMemoryLimit(-1); got != before {
		t.Errorf("limit = %d after Apply, want %d unchanged", got, before)
	}
	if gc := debug.SetGCPercent(150); gc != 150 {
		t.Errorf("GC percent = %d, want 150 unchanged", gc)
	}
}

func TestApplySetsLimitAndGC(t *testing.T) {
	restoreRuntime(t)
	unsetEnv(t, "GOMEMLIMIT")
	unsetEnv(t, "GOGC")

	need := Need{Workers: 1, ExifToolServers: 1, RetainedBytes: 256 * mib}
	Apply(need)
	want, _, _ := compute(totalRAM(), need)
	limit := debug.SetMemoryLimit(-1)
	if limit != want || limit <= 0 || limit == math.MaxInt64 {
		t.Errorf("limit = %d, want the computed %d", limit, want)
	}
	if gc := debug.SetGCPercent(-1); gc != -1 {
		t.Errorf("GC percent = %d, want -1", gc)
	}
}

func TestApplyKeepsGOGC(t *testing.T) {
	restoreRuntime(t)
	unsetEnv(t, "GOMEMLIMIT")
	t.Setenv("GOGC", "100")
	debug.SetGCPercent(100)

	Apply(Need{Workers: 1, ExifToolServers: 1})
	if gc := debug.SetGCPercent(100); gc != 100 {
		t.Errorf("GC percent = %d, want 100 kept", gc)
	}
}

// unsetEnv removes key for the test and restores it afterwards.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "") // registers the restore
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
}
