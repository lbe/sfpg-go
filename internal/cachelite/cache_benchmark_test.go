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
