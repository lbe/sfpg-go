package config

import (
	"regexp"
	"testing"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
)

func TestDefaultConfig_EnableCachePreload(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.EnableCachePreload {
		t.Error("expected EnableCachePreload default true, got false")
	}
}

// TestRecoverFromCorruption_EnableCachePreload verifies RecoverFromCorruption includes EnableCachePreload.

func TestRecoverFromCorruption_EnableCachePreload(t *testing.T) {
	t.Run("recovers value from defaults", func(t *testing.T) {
		defaults := DefaultConfig()
		defaults.EnableCachePreload = false
		cfg := DefaultConfig()
		cfg.EnableCachePreload = true // corrupt to different value
		cfg.RecoverFromCorruption(defaults)
		if cfg.EnableCachePreload {
			t.Error("expected EnableCachePreload false after recovery, got true")
		}
	})

	t.Run("nil defaults leaves config unchanged", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.SiteName = "Preserved"
		cfg.RecoverFromCorruption(nil)
		if cfg.SiteName != "Preserved" {
			t.Errorf("SiteName = %q, want 'Preserved'", cfg.SiteName)
		}
	})
}

// TestDefaultConfig_MaxHTTPCacheEntryInsertPerTransaction verifies the field exists with default 10.

func TestDefaultConfig_MaxHTTPCacheEntryInsertPerTransaction(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxHTTPCacheEntryInsertPerTransaction != 10 {
		t.Errorf("expected MaxHTTPCacheEntryInsertPerTransaction default 10, got %d", cfg.MaxHTTPCacheEntryInsertPerTransaction)
	}
}

// TestRecoverFromCorruption_MaxHTTPCacheEntryInsertPerTransaction verifies RecoverFromCorruption includes the field.

func TestRecoverFromCorruption_MaxHTTPCacheEntryInsertPerTransaction(t *testing.T) {
	defaults := DefaultConfig()
	defaults.MaxHTTPCacheEntryInsertPerTransaction = 25
	cfg := DefaultConfig()
	cfg.MaxHTTPCacheEntryInsertPerTransaction = 1
	cfg.RecoverFromCorruption(defaults)
	if cfg.MaxHTTPCacheEntryInsertPerTransaction != 25 {
		t.Errorf("expected MaxHTTPCacheEntryInsertPerTransaction 25 after recovery, got %d", cfg.MaxHTTPCacheEntryInsertPerTransaction)
	}
}

// TestDefaultConfig verifies that DefaultConfig returns correct default values.

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	// Verify server settings
	if cfg.ListenerAddress != "0.0.0.0" {
		t.Errorf("expected ListenerAddress to be '0.0.0.0', got %q", cfg.ListenerAddress)
	}
	if cfg.ListenerPort != 8081 {
		t.Errorf("expected ListenerPort to be 8081, got %d", cfg.ListenerPort)
	}

	// Verify log settings
	if cfg.LogLevel != "debug" {
		t.Errorf("expected LogLevel to be 'debug', got %q", cfg.LogLevel)
	}
	if cfg.LogRetentionCount != 7 {
		t.Errorf("expected LogRetentionCount to be 7, got %d", cfg.LogRetentionCount)
	}

	// Verify database settings
	if cfg.DBMaxPoolSize != 100 {
		t.Errorf("expected DBMaxPoolSize to be 100, got %d", cfg.DBMaxPoolSize)
	}
	if cfg.DBMinIdleConnections != 10 {
		t.Errorf("expected DBMinIdleConnections to be 10, got %d", cfg.DBMinIdleConnections)
	}
	if cfg.DBPoolMonitorInterval != 1*time.Minute {
		t.Errorf("expected DBPoolMonitorInterval to be 1m, got %v", cfg.DBPoolMonitorInterval)
	}

	// Verify cache settings
	if cfg.CacheMaxSize != 500*1024*1024 {
		t.Errorf("expected CacheMaxSize to be 500MB, got %d", cfg.CacheMaxSize)
	}
	if cfg.CacheMaxTime != 30*24*time.Hour {
		t.Errorf("expected CacheMaxTime to be 30 days, got %v", cfg.CacheMaxTime)
	}

	// Verify worker pool settings
	if cfg.WorkerPoolMax != 0 { // 0 means auto-calculate
		t.Errorf("expected WorkerPoolMax to be 0 (auto), got %d", cfg.WorkerPoolMax)
	}
	if cfg.WorkerPoolMinIdle != 0 { // 0 means auto-calculate
		t.Errorf("expected WorkerPoolMinIdle to be 0 (auto), got %d", cfg.WorkerPoolMinIdle)
	}
}

func TestDefaultConfig_IncludesETagVersion(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.ETagVersion == "" {
		t.Error("DefaultConfig() ETagVersion is empty, want default value")
	}

	// Verify it matches expected format YYYYMMDD-NN
	pattern := `^\d{8}-\d{2}$`
	matched, err := regexp.MatchString(pattern, cfg.ETagVersion)
	if err != nil {
		t.Fatalf("regexp error: %v", err)
	}
	if !matched {
		t.Errorf("ETagVersion = %q, does not match pattern %q", cfg.ETagVersion, pattern)
	}
}

func TestDiscoveryEnabled_ByDefault(t *testing.T) {
	defaults := DefaultConfig()
	if !defaults.RunFileDiscovery {
		t.Fatal("RunFileDiscovery should be true by default in config.DefaultConfig()")
	}
}

// TestConfigExport_ToFile_ShowsDiff verifies that exporting to file shows current vs new YAML content.
