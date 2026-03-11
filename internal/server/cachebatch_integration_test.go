//go:build integration

package server

import (
	"context"
	"testing"

	"github.com/lbe/sfpg-go/internal/server/cachebatch"
)

// TestCacheBatchLoad_BlocksWhenDiscoveryActive verifies batch load returns
// ErrDiscoveryActive when discovery is active in the database.
func TestCacheBatchLoad_BlocksWhenDiscoveryActive(t *testing.T) {
	t.Setenv("SEPG_SESSION_SECRET", "test-secret")

	app := CreateApp(t, false)
	defer app.Shutdown()

	if app.moduleStateService == nil {
		t.Fatal("moduleStateService not initialized")
	}

	ctx := context.Background()
	if err := app.moduleStateService.SetActive(ctx, "discovery", true); err != nil {
		t.Fatalf("SetActive(discovery, true): %v", err)
	}
	defer app.moduleStateService.SetActive(ctx, "discovery", false)

	cfg := cachebatch.Config{
		GetQueries: func() (cachebatch.BatchLoadQueries, func()) {
			cpc, err := app.dbRoPool.Get()
			if err != nil {
				return nil, nil
			}
			return cpc.Queries, func() { app.dbRoPool.Put(cpc) }
		},
		GetHandler:         app.getRouter,
		GetETagVersion:     func() string { return "v1" },
		ModuleStateService: app.moduleStateService,
	}

	mgr := cachebatch.NewManager(cfg)
	err := mgr.Run(ctx)
	if err != cachebatch.ErrDiscoveryActive {
		t.Errorf("Run() = %v, want ErrDiscoveryActive", err)
	}
}
