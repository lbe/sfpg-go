package tableswap

import "testing"

func TestDestAndStaleNamesAreDerivedFromBaseTableName(t *testing.T) {
	cases := []struct {
		base      string
		wantDest  string
		wantStale string
	}{
		{
			base:      "http_cache",
			wantDest:  "http_cache_new",
			wantStale: "http_cache_to_be_dropped",
		},
		{
			base:      "file_folder_index",
			wantDest:  "file_folder_index_new",
			wantStale: "file_folder_index_to_be_dropped",
		},
	}

	for _, tc := range cases {
		gotDest := destName(tc.base)
		if gotDest != tc.wantDest {
			t.Errorf("destName(%q) = %q, want %q", tc.base, gotDest, tc.wantDest)
		}

		gotStale := staleName(tc.base)
		if gotStale != tc.wantStale {
			t.Errorf("staleName(%q) = %q, want %q", tc.base, gotStale, tc.wantStale)
		}
	}
}
