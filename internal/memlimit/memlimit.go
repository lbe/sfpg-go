// Package memlimit sets the Go soft memory limit from the machine's RAM and
// what the process needs memory for, and turns GC percent off unless GOGC is
// set, so the limit alone paces collection.
package memlimit

import (
	"context"
	"log/slog"
	"os"
	"runtime/debug"
)

const (
	mib      = 1 << 20
	headroom = 96 * mib
)

// Need is what the process needs memory for.
type Need struct {
	Workers         int   // discovery workers
	ExifToolServers int   // metadata.Extractor.WASMServers()
	RetainedBytes   int64 // thumbnail.RetainedBytes
}

// Limit is the process memory limit set by Apply.
type Limit struct {
	Workers int // the worker count that fits the limit: Need.Workers, or 1
}

// Apply sets the runtime memory limit and GC policy for need and returns the
// limit. With GOMEMLIMIT set it changes nothing and keeps need.Workers.
// Call it once, at startup.
func Apply(need Need) *Limit {
	ram := totalRAM()
	ceiling := ceilingFor(ram)
	l := &Limit{Workers: need.Workers}
	if _, ok := os.LookupEnv("GOMEMLIMIT"); ok {
		slog.Info("memory limit", "limit_source", "env", "limit", debug.SetMemoryLimit(-1),
			"ram", ram, "ceiling", ceiling, "workers", need.Workers, "exiftool_servers", need.ExifToolServers)
		return l
	}

	var limit int64
	var source string
	limit, l.Workers, source = compute(ram, need)
	debug.SetMemoryLimit(limit)
	gc := "env"
	if _, ok := os.LookupEnv("GOGC"); !ok {
		debug.SetGCPercent(-1)
		gc = "off"
	}
	level := slog.LevelInfo
	if source != "computed" {
		level = slog.LevelWarn
	}
	slog.Log(context.Background(), level, "memory limit", "limit_source", source, "limit", limit,
		"gc", gc, "ram", ram, "ceiling", ceiling, "workers", l.Workers, "exiftool_servers", need.ExifToolServers)
	return l
}

// compute returns the limit in bytes, the worker count that fits it, and how
// it was chosen. A ram of 0 means unknown, so there is no ceiling.
func compute(ram uint64, need Need) (int64, int, string) {
	ceiling := ceilingFor(ram)
	if limit := floorFor(need.Workers, need) + headroom; ram == 0 || limit <= ceiling {
		return limit, need.Workers, "computed"
	}
	if limit := floorFor(1, need) + headroom; limit <= ceiling {
		return limit, 1, "workers_reduced"
	}
	return ceiling, 1, "capped"
}

// floorFor estimates the least memory the process needs with the given
// number of workers.
func floorFor(workers int, need Need) int64 {
	return 20*mib + int64(workers)*13*mib + int64(need.ExifToolServers)*64*mib + need.RetainedBytes
}

// ceilingFor returns 75% of ram, or 0 when ram is unknown.
func ceilingFor(ram uint64) int64 {
	if ram == 0 {
		return 0
	}
	return int64(ram * 75 / 100)
}
