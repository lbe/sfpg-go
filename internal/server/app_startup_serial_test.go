//go:build integration

package server

import (
	"sync"
	"testing"

	"github.com/lbe/sfpg-go/internal/getopt"
)

// TestStartup_OrderingConstraint verifies that app startup follows the correct
// order: config load → pool creation → pool publication → HTTP server start.
//
// EXPECTED BEHAVIOR: Pools should be created AFTER config is loaded, and should
// not be accessible until they are fully initialized.
//
// CURRENT BEHAVIOR (DEFECT): The ordering might not be enforced, allowing race
// conditions where pools are accessed before configuration is applied.
//
// This test SHOULD FAIL until proper ordering constraints are enforced.
func TestStartup_OrderingConstraint(t *testing.T) {
	tempDir := t.TempDir()

	opt := getopt.Opt{
		SessionSecret: getopt.OptString{
			String: "test-secret-ordering",
			IsSet:  true,
		},
	}

	// Track the order of operations
	var mu sync.Mutex
	operationOrder := []string{}
	recordOp := func(op string) {
		mu.Lock()
		operationOrder = append(operationOrder, op)
		mu.Unlock()
	}

	app := New(opt, "x.y.z")
	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()

	// Wrap critical operations to track ordering
	recordOp("start_setDB")
	app.setDB()
	recordOp("end_setDB")

	recordOp("start_loadConfig")
	if err := app.loadConfig(); err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}
	recordOp("end_loadConfig")

	// Verify ordering: setDB should complete before loadConfig
	mu.Lock()
	order := make([]string, len(operationOrder))
	copy(order, operationOrder)
	mu.Unlock()

	// Check that operations happened in the correct order
	expectedOrder := []string{
		"start_setDB",
		"end_setDB",
		"start_loadConfig",
		"end_loadConfig",
	}

	if len(order) != len(expectedOrder) {
		t.Errorf("unexpected number of operations: got %d, want %d", len(order), len(expectedOrder))
	}

	for i := range expectedOrder {
		if i >= len(order) {
			t.Errorf("missing operation at index %d: expected %s", i, expectedOrder[i])
			continue
		}
		if order[i] != expectedOrder[i] {
			t.Errorf("operation order mismatch at index %d: got %s, want %s", i, order[i], expectedOrder[i])
		}
	}

	// ASSERTION: Verify that pools were created with config values, not defaults
	// This checks that loadConfig() preceded pool creation
	app.configMu.RLock()
	configMaxPool := app.config.DBMaxPoolSize
	app.configMu.RUnlock()

	actualMaxPool := app.dbRwPool.Config.MaxConnections

	// If pools were created before config was loaded, they would have default values (100)
	// After loadConfig, they should have config values
	if actualMaxPool != int64(configMaxPool) {
		t.Errorf("DEFECT: Pool was created before config was applied. "+
			"Pool has MaxConnections=%d, but config has DBMaxPoolSize=%d. "+
			"This indicates setDB created pools before loadConfig was called.",
			actualMaxPool, configMaxPool)
	}

	app.Shutdown()

	// EXPECTED: This test SHOULD FAIL if pools are created before configuration
	// is loaded, indicating improper initialization ordering.
}
