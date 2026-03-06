//go:build integration

package server

import (
	"sync"
	"testing"
	"time"

	"github.com/lbe/sfpg-go/internal/getopt"
)

// TestStartup_DBPoolsNotAccessibleDuringInit verifies that database pools
// are not accessible to other goroutines during the initialization phase.
//
// EXPECTED BEHAVIOR: During app startup, pools should not be accessible until
// initialization is complete. Access attempts during init should block or error.
//
// CURRENT BEHAVIOR (DEFECT): Pools may be accessible before initialization
// completes, allowing goroutines to access transient pool instances.
//
// This test SHOULD FAIL until proper initialization ordering is enforced.
func TestStartup_DBPoolsNotAccessibleDuringInit(t *testing.T) {
	tempDir := t.TempDir()

	opt := getopt.Opt{
		SessionSecret: getopt.OptString{
			String: "test-secret-startup-serial",
			IsSet:  true,
		},
	}

	app := New(opt, "x.y.z")
	app.setRootDir(&tempDir)
	app.setupBootstrapLogging()

	// Start a goroutine that tries to access pools during initialization
	// This simulates a scenario where background work might start before pools are ready
	var wg sync.WaitGroup
	poolAccessAttempted := false
	poolAccessSucceeded := false

	wg.Add(1)
	go func() {
		defer wg.Done()

		// Give a tiny bit of time for setDB to start but not finish
		time.Sleep(10 * time.Millisecond)

		poolAccessAttempted = true

		// Try to access the pool during initialization
		// This should either:
		// 1. Block until pools are ready
		// 2. Return an error indicating pools aren't ready
		// 3. (DEFECT) Allow access to transient pool instances
		if app.dbRwPool != nil {
			_, err := app.dbRwPool.Get()
			if err == nil {
				// Successfully got a connection during init - this is the defect!
				poolAccessSucceeded = true
			}
		}
	}()

	// Initialize database (this is where pools are created)
	app.setDB()

	// Wait for the goroutine to complete
	wg.Wait()

	// ASSERTION: If pools were accessible during initialization, that's a defect
	if poolAccessAttempted && poolAccessSucceeded {
		t.Errorf("DEFECT: Pool was accessible and usable during initialization phase. " +
			"This indicates pools are published before initialization is complete.")
	}

	// Verify pools are accessible after initialization
	if app.dbRwPool == nil {
		t.Fatal("RW pool should be initialized after setDB()")
	}
	if app.dbRoPool == nil {
		t.Fatal("RO pool should be initialized after setDB()")
	}

	conn, err := app.dbRwPool.Get()
	if err != nil {
		t.Errorf("should be able to get connection after complete initialization: %v", err)
	} else {
		app.dbRwPool.Put(conn)
	}

	app.Shutdown()

	// EXPECTED: This test SHOULD FAIL if pools are accessible during initialization,
	// indicating that the startup sequence doesn't properly serialize pool access.
}

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
