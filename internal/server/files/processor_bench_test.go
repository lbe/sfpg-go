package files

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.local/sfpg/internal/queue"
	"go.local/sfpg/internal/workerpool"
)

// BenchmarkWorkerPoolProcessing measures throughput: files processed per second
func BenchmarkWorkerPoolProcessing(b *testing.B) {
	for _, numFiles := range []int{100, 1000} {
		b.Run(fmt.Sprintf("files_%d", numFiles), func(b *testing.B) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			q := queue.NewQueue[string](numFiles)
			for i := range numFiles {
				_ = q.Enqueue(fmt.Sprintf("/images/file_%d.jpg", i))
			}

			fp := &fakeProcessor{}
			pool := workerpool.NewPool(ctx, 1, 1, 10*time.Second)
			pool.Stats.RunningWorkers.Add(1)
			stats := &ProcessingStats{}

			poolFunc := NewPoolFuncWithProcessor(fp, q, "/images", benchRemovePrefix, stats)

			b.ResetTimer()
			b.ReportAllocs()

			done := make(chan error, 1)
			go func() {
				// Pass nil pool - before optimization this triggers per-file Get/Put
				// After optimization, worker gets connection once
				done <- poolFunc(ctx, pool, nil, nil, q.Len, 1)
			}()

			deadline := time.After(10 * time.Second)
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for pool.Stats.CompletedTasks.Load() < uint64(numFiles) {
				select {
				case <-deadline:
					b.Fatal("timeout")
				case <-ticker.C:
				}
			}
			cancel()
			<-done

			b.StopTimer()
			elapsed := b.Elapsed()
			filesPerSec := float64(numFiles) / elapsed.Seconds()
			b.ReportMetric(filesPerSec, "files/sec")
		})
	}
}

func benchRemovePrefix(normalizedDir, p string) (string, error) {
	normalizedDir = strings.TrimSuffix(normalizedDir, "/")
	if !strings.HasPrefix(p, normalizedDir+"/") {
		return p, nil
	}
	return strings.TrimPrefix(p, normalizedDir+"/"), nil
}
