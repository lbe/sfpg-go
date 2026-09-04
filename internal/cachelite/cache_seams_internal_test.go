package cachelite

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
)

func TestGetCacheSizeBytes_NullResult(t *testing.T) {
	orig := getHttpCacheSizeBytes
	getHttpCacheSizeBytes = func(ctx context.Context, cpc *dbconnpool.CpConn) (int64, error) {
		return 0, nil
	}
	t.Cleanup(func() { getHttpCacheSizeBytes = orig })

	size, err := GetCacheSizeBytes(context.Background(), createTestDBPoolInternal(t))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if size != 0 {
		t.Fatalf("expected size 0, got %d", size)
	}
}

func TestGetCacheSizeBytes_HookError(t *testing.T) {
	orig := getHttpCacheSizeBytes
	getHttpCacheSizeBytes = func(ctx context.Context, cpc *dbconnpool.CpConn) (int64, error) {
		return 0, fmt.Errorf("hook error: simulated database failure")
	}
	t.Cleanup(func() { getHttpCacheSizeBytes = orig })

	_, err := GetCacheSizeBytes(context.Background(), createTestDBPoolInternal(t))
	if err == nil {
		t.Fatal("expected error from hook")
	}
	if !strings.Contains(err.Error(), "hook error: simulated database failure") {
		t.Fatalf("expected error containing 'hook error: simulated database failure', got %v", err)
	}
}
