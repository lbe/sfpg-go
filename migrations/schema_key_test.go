package migrations

import (
	"strings"
	"testing"
)

func TestLatestSchemaKey(t *testing.T) {
	key, err := LatestSchemaKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "0") && len(key) < 3 {
		t.Fatalf("unexpected schema key format: %q", key)
	}
	if key != "023" {
		t.Fatalf("LatestSchemaKey() = %q, want 023 (update test when migrations advance)", key)
	}
}
