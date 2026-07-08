package config

import (
	"slices"
	"testing"
)

func TestFields(t *testing.T) {
	fields := Fields()
	if len(fields) == 0 {
		t.Fatal("Fields() returned empty slice")
	}

	wantKeys := []string{"listener_port", "image_directory", "enable_http_cache"}
	gotKeys := make([]string, len(fields))
	for i, f := range fields {
		gotKeys[i] = f.DBKey
		if f.Set == nil {
			t.Errorf("FieldInfo.Set for %q is nil", f.DBKey)
		}
	}
	for _, key := range wantKeys {
		if !slices.Contains(gotKeys, key) {
			t.Errorf("Fields() missing expected key %q, got %v", key, gotKeys)
		}
	}

	fields2 := Fields()
	if &fields[0] != &fields2[0] {
		t.Error("Fields() returned a different slice on second call, expected cached result")
	}
}
