//go:build linux

package rssmonitor

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/lbe/sfpg-go/internal/humanize"
)

func run(ctx context.Context, cfg Config) {
	interval := cfg.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	deltaThreshold := cfg.DeltaThreshold
	if deltaThreshold <= 0 {
		deltaThreshold = DefaultDeltaThreshold
	}
	minLogInterval := cfg.MinLogInterval
	if minLogInterval <= 0 {
		minLogInterval = DefaultMinLogInterval
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		var prevRSS uint64
		var havePrev bool
		var lastLog time.Time

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !slog.Default().Enabled(ctx, slog.LevelDebug) {
					continue
				}

				rss, ok := readProcessRSS()
				if !ok {
					continue
				}

				var delta int64
				if havePrev {
					delta = int64(rss) - int64(prevRSS)
				}
				prevRSS = rss
				havePrev = true

				now := time.Now()
				if havePrev && (delta > deltaThreshold || delta < -deltaThreshold) || now.Sub(lastLog) >= minLogInterval {
					var rwConns, roConns int64
					if cfg.NumRWConnections != nil {
						rwConns = cfg.NumRWConnections()
					}
					if cfg.NumROConnections != nil {
						roConns = cfg.NumROConnections()
					}
					heapInuse := readHeapInuse()
					rssMinusHeapInuse := int64(rss) - int64(heapInuse)
					slog.LogAttrs(ctx, slog.LevelDebug, "process rss sample",
						slog.String("rss", humanize.Comma(int64(rss)).String()),
						slog.String("delta_rss", humanize.Comma(delta).String()),
						slog.String("rss_minus_heap_inuse", humanize.Comma(rssMinusHeapInuse).String()),
						slog.Int64("rw_conns", rwConns),
						slog.Int64("ro_conns", roConns),
					)
					lastLog = now
				}
			}
		}
	}()
}

func readProcessRSS() (uint64, bool) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, false
	}
	kb, ok := parseVmRSSKB(data)
	if !ok {
		return 0, false
	}
	return kb * 1024, true
}

func readHeapInuse() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapInuse
}

func parseVmRSSKB(data []byte) (uint64, bool) {
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("VmRSS:")) {
			continue
		}
		fields := bytes.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		kb, err := strconv.ParseUint(string(fields[1]), 10, 64)
		if err != nil {
			return 0, false
		}
		return kb, true
	}
	return 0, false
}
