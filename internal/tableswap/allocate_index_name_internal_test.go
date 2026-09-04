package tableswap

import "testing"

// TestAllocateIndexNameStripsDigitsAndAvoidsExistingNames verifies the
// index-name allocation rule used during cache table rotations: the active
// base name is recovered by stripping any trailing _<digits> suffix, then a
// candidate is chosen so that it does not collide with a name already present
// in the provided (live) name set.
func TestAllocateIndexNameStripsDigitsAndAvoidsExistingNames(t *testing.T) {
	cases := []struct {
		active   string
		existing []string
		want     string
	}{
		// Empty set: the base is allocated directly.
		{
			active:   "http_cache",
			existing: nil,
			want:     "http_cache",
		},
		// Empty set expressed explicitly.
		{
			active:   "http_cache",
			existing: []string{},
			want:     "http_cache",
		},
		// {base} taken -> base_1.
		{
			active:   "http_cache",
			existing: []string{"http_cache"},
			want:     "http_cache_1",
		},
		// {base, base_1} taken -> base_2.
		{
			active:   "http_cache",
			existing: []string{"http_cache", "http_cache_1"},
			want:     "http_cache_2",
		},
		// Trailing _<digits> is stripped to recover the base, so the
		// candidate still derives from the original base, not the suffixed
		// input. Empty set -> base.
		{
			active:   "http_cache_1",
			existing: nil,
			want:     "http_cache",
		},
		// After only base_1 remains (base itself is free), the result is the
		// base: the free base always wins over the occupied suffixed names.
		{
			active:   "http_cache",
			existing: []string{"http_cache_1"},
			want:     "http_cache",
		},
		// Stripped base taken but the suffixed candidate free -> base_1.
		{
			active:   "http_cache_2",
			existing: []string{"http_cache"},
			want:     "http_cache_1",
		},
		// Many existing suffixed variants: find the first free slot.
		{
			active:   "http_cache",
			existing: []string{"http_cache", "http_cache_1", "http_cache_2"},
			want:     "http_cache_3",
		},
	}

	for _, tc := range cases {
		got := allocateIndexName(tc.active, tc.existing)
		if got != tc.want {
			t.Errorf("allocateIndexName(%q, %v) = %q, want %q", tc.active, tc.existing, got, tc.want)
		}
	}
}
