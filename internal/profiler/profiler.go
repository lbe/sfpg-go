// Package profiler provides profiling support for the application using github.com/pkg/profile.
// It supports various profiling modes including CPU, memory, allocations, heap, mutex, block, and trace.
package profiler

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/pkg/profile"
)

// Config holds profiling configuration.
type Config struct {
	// Mode specifies the profiling mode: cpu, mem, allocs, heap, mutex, block, trace.
	// Empty string disables profiling.
	Mode string
}

var (
	profileDir     string
	profileModeStr string
	mu             sync.RWMutex
)

// Dir returns the directory where profile artifacts were written, or empty string if profiling disabled.
func Dir() string {
	mu.RLock()
	defer mu.RUnlock()
	return profileDir
}

// Mode returns the active profiling mode or empty string if disabled.
func Mode() string {
	mu.RLock()
	defer mu.RUnlock()
	return profileModeStr
}

// Start initializes profiling based on the provided configuration.
// Returns a stop function that must be called to finalize profiling, and an error if initialization fails.
// The stop function is safe to call even if profiling was not started.
func Start(cfg Config) (stop func(), err error) {
	if cfg.Mode == "" {
		return func() {}, nil
	}

	mode := strings.ToLower(cfg.Mode)
	var profileMode func(*profile.Profile)
	var memProfileRate *int // set for allocs/heap so we don't leave runtime.MemProfileRate at 0

	switch mode {
	case "cpu":
		profileMode = profile.CPUProfile
	case "mem":
		profileMode = profile.MemProfile
	case "allocs":
		profileMode = profile.MemProfileAllocs
		// pkg/profile does not set MemProfileRate for MemProfileAllocs; it leaves 0, which disables sampling
		// and can change runtime behavior (e.g. gallery layout breaks). Set explicit rate so allocs are
		// recorded and runtime behavior matches non-profiling runs.
		rate := 512 * 1024
		memProfileRate = &rate
	case "heap":
		profileMode = profile.MemProfileHeap
		// Same as allocs: pkg/profile does not set MemProfileRate for MemProfileHeap; set explicitly
		// so the profile records data and runtime behavior matches non-profiling runs.
		rate := 512 * 1024
		memProfileRate = &rate
	case "mutex":
		profileMode = profile.MutexProfile
	case "block":
		profileMode = profile.BlockProfile
	case "trace":
		profileMode = profile.TraceProfile
	default:
		return nil, fmt.Errorf("invalid profile mode: %s (valid: cpu, mem, allocs, heap, mutex, block, trace)", cfg.Mode)
	}

	// Create a temp directory to capture the profile output path for logging.
	// This keeps outputs under /tmp while giving us a deterministic location to report.
	tempDir, mkErr := os.MkdirTemp("", "profile")
	if mkErr != nil {
		return nil, fmt.Errorf("failed to create temporary profile dir: %w", mkErr)
	}
	mu.Lock()
	profileDir = tempDir
	profileModeStr = mode
	mu.Unlock()

	// Determine profile filename based on mode
	var profileFile string
	switch mode {
	case "cpu":
		profileFile = "cpu.pprof"
	case "mem", "heap":
		profileFile = "mem.pprof"
	case "allocs":
		profileFile = "mem.pprof"
	case "mutex":
		profileFile = "mutex.pprof"
	case "block":
		profileFile = "block.pprof"
	case "trace":
		profileFile = "trace.out"
	default:
		profileFile = "profile.pprof"
	}

	slog.Info("Starting profiler", "mode", mode, "file", tempDir+"/"+profileFile)
	opts := []func(*profile.Profile){profileMode, profile.NoShutdownHook, profile.ProfilePath(tempDir), profile.Quiet}
	if memProfileRate != nil {
		opts = append(opts, profile.MemProfileRate(*memProfileRate))
	}
	prof := profile.Start(opts...)

	return func() {
		prof.Stop()
		// Determine profile filename based on mode for shutdown log
		var profileFile string
		switch mode {
		case "cpu":
			profileFile = "cpu.pprof"
		case "mem", "heap":
			profileFile = "mem.pprof"
		case "allocs":
			profileFile = "mem.pprof"
		case "mutex":
			profileFile = "mutex.pprof"
		case "block":
			profileFile = "block.pprof"
		case "trace":
			profileFile = "trace.out"
		default:
			profileFile = "profile.pprof"
		}
		slog.Info("Profiler stopped", "mode", mode, "file", tempDir+"/"+profileFile)
	}, nil
}
