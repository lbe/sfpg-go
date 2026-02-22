package cachelite

import (
	"context"
	"database/sql"
	"math/rand"
	"strconv"
	"testing"
	"time"
)

// representativePoolSize is the number of pre-built entries matching production content_length distribution.
const representativePoolSize = 1000

// makeRepresentativeEntryPool creates 1000 entries with body sizes approximating production distribution:
//   - < 1 KB (1.2%): 12 entries @ ~750 bytes
//   - 1–2 KB (3.0%): 30 entries @ ~1.5 KB
//   - 2–5 KB (45.8%): 458 entries @ ~3.5 KB
//   - 5–10 KB (48.7%): 487 entries @ ~7.5 KB
//   - 25–50 KB (0.6%): 6 entries @ ~38 KB
//   - 50–100 KB (0.3%): 3 entries @ ~75 KB
//   - 100–250 KB (0.3%): 3 entries @ ~175 KB
//   - 500 KB–1 MB (0.0%): 1 entry @ ~750 KB
func makeRepresentativeEntryPool(now int64) []*HTTPCacheEntry {
	nullStr := sql.NullString{}
	nullInt64 := sql.NullInt64{}

	sizes := []struct {
		count int
		bytes int
	}{
		{12, 750},   // < 1 KB
		{30, 1500},  // 1–2 KB
		{458, 3500}, // 2–5 KB
		{487, 7500}, // 5–10 KB
		{6, 38000},  // 25–50 KB
		{3, 75000},  // 50–100 KB
		{3, 175000}, // 100–250 KB
		{1, 750000}, // 500 KB–1 MB
	}
	pool := make([]*HTTPCacheEntry, 0, representativePoolSize)
	idx := 0
	for _, s := range sizes {
		for i := 0; i < s.count; i++ {
			body := make([]byte, s.bytes)
			for j := range body {
				body[j] = byte((idx + j) & 0xff)
			}
			pool = append(pool, &HTTPCacheEntry{
				Key:           "pool-" + strconv.Itoa(idx),
				Method:        "GET",
				Path:          "/info/image/1",
				Status:        200,
				Body:          body,
				CreatedAt:     now,
				QueryString:   nullStr,
				ContentType:   nullStr,
				CacheControl:  nullStr,
				ETag:          nullStr,
				LastModified:  nullStr,
				Vary:          nullStr,
				ContentLength: nullInt64,
				ExpiresAt:     nullInt64,
			})
			idx++
		}
	}
	return pool
}

// batchBenchConfigs defines [totalEntries, batchSize] pairs for benchmarks.
// [100, 50] = 100 total entries written in batches of 50 (2 batches per iteration).
var batchBenchConfigs = [][2]int{
	{1, 5}, {1, 5}, {5, 5}, {10, 5}, {25, 5}, {50, 5}, {75, 5}, {100, 5},
	{1, 10}, {1, 10}, {5, 10}, {10, 10}, {25, 10}, {50, 10}, {75, 10}, {100, 10},
	{1, 15}, {1, 15}, {5, 15}, {10, 15}, {25, 15}, {50, 15}, {75, 15}, {100, 15},
	{1, 25}, {1, 25}, {5, 25}, {10, 25}, {25, 25}, {50, 25}, {75, 25}, {100, 25},
	{1, 50}, {1, 50}, {5, 50}, {10, 50}, {25, 50}, {50, 50}, {75, 50}, {100, 50},
}

// BenchmarkStoreCacheEntryBatch runs sub-benchmarks for each [totalEntries, batchSize] config.
// Uses a pool of 1000 entries with production-like content_length distribution; randomly
// selects entries for each batch.
func BenchmarkStoreCacheEntryBatch(b *testing.B) {
	db := createTestDBPoolTB(b)
	ctx := context.Background()
	now := time.Now().Unix()
	pool := makeRepresentativeEntryPool(now)

	// Pre-allocate batch structs (max batch size 100)
	batchStructs := make([]HTTPCacheEntry, 100)
	batch := make([]*HTTPCacheEntry, 100)
	for i := range batch {
		batch[i] = &batchStructs[i]
	}

	rng := rand.New(rand.NewSource(42))

	for _, cfg := range batchBenchConfigs {
		totalEntries, batchSize := cfg[0], cfg[1]
		b.Run(strconv.Itoa(totalEntries)+"_"+strconv.Itoa(batchSize), func(b *testing.B) {
			numBatches := (totalEntries + batchSize - 1) / batchSize

			used := make(map[int]struct{}, batchSize)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				offset := 0
				for batchNum := range numBatches {
					rem := totalEntries - offset
					thisBatch := min(rem, batchSize)
					// Clear used map for this batch
					for k := range used {
						delete(used, k)
					}
					// Select thisBatch unique random indices from pool
					for j := range thisBatch {
						for {
							idx := rng.Intn(representativePoolSize)
							if _, ok := used[idx]; !ok {
								used[idx] = struct{}{}
								*batch[j] = *pool[idx]
								batch[j].Key = "bench-" + strconv.Itoa(i) + "-" + strconv.Itoa(batchNum) + "-" + strconv.Itoa(j)
								break
							}
						}
					}
					if err := StoreCacheEntryBatch(ctx, db, batch[:thisBatch]); err != nil {
						b.Fatal(err)
					}
					offset += thisBatch
				}
			}
		})
	}
}

// BenchmarkStoreCacheEntry_Single benchmarks a single StoreCacheEntry using representative body sizes.
func BenchmarkStoreCacheEntry_Single(b *testing.B) {
	db := createTestDBPoolTB(b)
	ctx := context.Background()
	now := time.Now().Unix()
	pool := makeRepresentativeEntryPool(now)
	rng := rand.New(rand.NewSource(42))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := rng.Intn(representativePoolSize)
		entry := pool[idx]
		// Copy and assign unique key (pool entry shared, so we need a copy for Key mutation)
		key := "bench-single-" + strconv.Itoa(i)
		dup := *entry
		dup.Key = key
		if err := StoreCacheEntry(ctx, db, &dup); err != nil {
			b.Fatal(err)
		}
	}
}
