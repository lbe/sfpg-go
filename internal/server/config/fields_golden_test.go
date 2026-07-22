package config

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestGoldenBaseline captures the pre-refactor (and intended post-refactor)
// behavior of ToMap, ExportToYAML, IdentifyChanges, restartRequiredKeys,
// MergeDefaults, and RecoverFromCorruption. The sentinel values are chosen
// to be obviously different from defaults so regressions are visible.
func TestGoldenBaseline(t *testing.T) {
	// goldenConfig is a fully-populated Config with non-default sentinel values.
	goldenConfig := &Config{
		ListenerAddress:                       "golden-listener",
		ListenerPort:                          31337,
		LogDirectory:                          "/tmp/sfpg-golden-test",
		LogLevel:                              "error",
		LogRollover:                           "daily",
		LogRetentionCount:                     99,
		SiteName:                              "GoldenSite",
		Themes:                                []string{"g1", "g2"},
		CurrentTheme:                          "golden-theme",
		ImageDirectory:                        "/tmp/sfpg-golden-images",
		ETagVersion:                           "20990101-99",
		SessionMaxAge:                         999999,
		SessionHttpOnly:                       false,
		SessionSecure:                         false,
		SessionSameSite:                       "Strict",
		EnableHTTPCache:                       false,
		CacheMaxSize:                          9999999999,
		CacheMaxTime:                          42 * time.Minute,
		CacheMaxEntrySize:                     8888888888,
		CacheCleanupInterval:                  7 * time.Minute,
		DBMaxPoolSize:                         77,
		DBMinIdleConnections:                  11,
		DBOptimizeInterval:                    3 * time.Hour,
		WorkerPoolMax:                         55,
		WorkerPoolMinIdle:                     5,
		WorkerPoolMaxIdleTime:                 99 * time.Second,
		DBPoolMonitorInterval:                 2 * time.Minute,
		QueueSize:                             12345,
		EnableCachePreload:                    false,
		MaxHTTPCacheEntryInsertPerTransaction: 77,
		HTTPCacheBodyCodec:                    "zstd-1",
		LockoutDuration:                       7200,
		LockoutThreshold:                      5,
		LoginRateLimitPerIP:                   20,
		RunFileDiscovery:                      false,
		EnablePprof:                           true,
		DiscoveryQueueMax:                     10000,
	}

	// zeroConfig is used for MergeDefaults / RecoverFromCorruption tests.
	zeroConfig := &Config{}

	// otherConfig differs from goldenConfig on every field.
	otherConfig := &Config{
		ListenerAddress:                       "other-listener",
		ListenerPort:                          44444,
		LogDirectory:                          "/tmp/other-logs",
		LogLevel:                              "warn",
		LogRollover:                           "monthly",
		LogRetentionCount:                     42,
		SiteName:                              "OtherSite",
		Themes:                                []string{"a", "b", "c"},
		CurrentTheme:                          "other-theme",
		ImageDirectory:                        "/tmp/other-images",
		ETagVersion:                           "20000101-00",
		SessionMaxAge:                         111111,
		SessionHttpOnly:                       true,
		SessionSecure:                         true,
		SessionSameSite:                       "Lax",
		EnableHTTPCache:                       true,
		CacheMaxSize:                          1111111111,
		CacheMaxTime:                          10 * time.Minute,
		CacheMaxEntrySize:                     2222222222,
		CacheCleanupInterval:                  3 * time.Minute,
		DBMaxPoolSize:                         33,
		DBMinIdleConnections:                  7,
		DBOptimizeInterval:                    5 * time.Hour,
		WorkerPoolMax:                         88,
		WorkerPoolMinIdle:                     3,
		WorkerPoolMaxIdleTime:                 50 * time.Second,
		DBPoolMonitorInterval:                 4 * time.Minute,
		QueueSize:                             99999,
		EnableCachePreload:                    true,
		MaxHTTPCacheEntryInsertPerTransaction: 33,
		HTTPCacheBodyCodec:                    "gzip-6",
		LockoutDuration:                       7777,
		LockoutThreshold:                      99,
		LoginRateLimitPerIP:                   50,
		RunFileDiscovery:                      true,
		EnablePprof:                           false,
		DiscoveryQueueMax:                     50000,
	}

	t.Run("ToMap", func(t *testing.T) {
		got := goldenConfig.ToMap()
		want := map[string]string{
			"listener_address":          "golden-listener",
			"listener_port":             "31337",
			"log_directory":             "/tmp/sfpg-golden-test",
			"log_level":                 "error",
			"log_rollover":              "daily",
			"log_retention_count":       "99",
			"site_name":                 "GoldenSite",
			"themes":                    `["g1","g2"]`,
			"current_theme":             "golden-theme",
			"image_directory":           "/tmp/sfpg-golden-images",
			"etag_version":              "20990101-99",
			"session_max_age":           "999999",
			"session_http_only":         "false",
			"session_secure":            "false",
			"session_same_site":         "Strict",
			"enable_http_cache":         "false",
			"cache_max_size":            "9999999999",
			"cache_max_time":            "42m0s",
			"cache_max_entry_size":      "8888888888",
			"cache_cleanup_interval":    "7m0s",
			"db_max_pool_size":          "77",
			"db_min_idle_connections":   "11",
			"db_optimize_interval":      "3h0m0s",
			"worker_pool_max":           "55",
			"worker_pool_min_idle":      "5",
			"worker_pool_max_idle_time": "1m39s",
			"db_pool_monitor_interval":  "2m0s",
			"queue_size":                "12345",
			"enable_cache_preload":      "false",
			"max_http_cache_entry_insert_per_transaction": "77",
			"http_cache_body_codec":                       "zstd-1",
			"lockout_duration":                            "7200",
			"lockout_threshold":                           "5",
			"login_rate_limit_per_ip":                     "20",
			"run_file_discovery":                          "false",
			"enable_pprof":                                "true",
			"discovery_queue_max":                         "10000",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ToMap mismatch")
			for k, w := range want {
				g, ok := got[k]
				if !ok {
					t.Errorf("  missing key %q", k)
				} else if g != w {
					t.Errorf("  %s: got %q, want %q", k, g, w)
				}
			}
			for k := range got {
				if _, ok := want[k]; !ok {
					t.Errorf("  extra key %q", k)
				}
			}
		}
	})

	t.Run("ExportToYAML", func(t *testing.T) {
		gotYAML, err := goldenConfig.ExportToYAML()
		if err != nil {
			t.Fatalf("ExportToYAML error: %v", err)
		}
		var got map[string]any
		if err := yaml.Unmarshal([]byte(gotYAML), &got); err != nil {
			t.Fatalf("yaml.Unmarshal error: %v", err)
		}
		want := map[string]any{
			"listener-address":          "golden-listener",
			"listener-port":             31337,
			"log-directory":             "/tmp/sfpg-golden-test",
			"log-level":                 "error",
			"log-rollover":              "daily",
			"log-retention-count":       99,
			"site-name":                 "GoldenSite",
			"themes":                    []any{"g1", "g2"},
			"current-theme":             "golden-theme",
			"image-directory":           "/tmp/sfpg-golden-images",
			"etag-version":              "20990101-99",
			"session-max-age":           999999,
			"session-http-only":         false,
			"session-secure":            false,
			"session-same-site":         "Strict",
			"http-cache":                false,
			"cache-max-size":            9999999999,
			"cache-max-time":            "42m0s",
			"cache-max-entry-size":      8888888888,
			"cache-cleanup-interval":    "7m0s",
			"db-max-pool-size":          77,
			"db-min-idle-connections":   11,
			"db-optimize-interval":      "3h0m0s",
			"worker-pool-max":           55,
			"worker-pool-min-idle":      5,
			"worker-pool-max-idle-time": "1m39s",
			"db-pool-monitor-interval":  "2m0s",
			"queue-size":                12345,
			"enable-cache-preload":      false,
			"max-http-cache-entry-insert-per-transaction": 77,
			"http-cache-body-codec":                       "zstd-1",
			"lockout-duration":                            7200,
			"lockout-threshold":                           5,
			"login-rate-limit-per-ip":                     20,
			"discover":                                    false,
			"enable-pprof":                                true,
			"discovery-queue-max":                         10000,
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ExportToYAML parsed mismatch")
			for k, w := range want {
				g, ok := got[k]
				if !ok {
					t.Errorf("  missing key %q", k)
				} else if !reflect.DeepEqual(g, w) {
					t.Errorf("  %s: got %v (%T), want %v (%T)", k, g, g, w, w)
				}
			}
			for k := range got {
				if _, ok := want[k]; !ok {
					t.Errorf("  extra key %q", k)
				}
			}
		}
	})

	t.Run("IdentifyChanges", func(t *testing.T) {
		got := goldenConfig.IdentifyChanges(otherConfig)
		slices.Sort(got)
		// Post-refactor: all yamlKeys including "themes".
		want := []string{
			"cache-cleanup-interval", "cache-max-entry-size", "cache-max-size",
			"cache-max-time", "current-theme",
			"db-max-pool-size", "db-min-idle-connections", "db-optimize-interval",
			"db-pool-monitor-interval", "discover", "enable-cache-preload",
			"enable-pprof",
			"discovery-queue-max",
			"etag-version",
			"http-cache", "image-directory", "listener-address", "listener-port",
			"log-directory", "log-level", "log-retention-count", "log-rollover",
			"lockout-duration", "lockout-threshold", "login-rate-limit-per-ip", "max-http-cache-entry-insert-per-transaction", "queue-size",
			"session-http-only", "session-max-age", "session-same-site",
			"session-secure", "site-name", "themes", "worker-pool-max",
			"worker-pool-max-idle-time", "worker-pool-min-idle",
			"http-cache-body-codec",
		}
		slices.Sort(want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("IdentifyChanges mismatch\ngot:  %v\nwant: %v", got, want)
		}
	})

	t.Run("restartRequiredKeys", func(t *testing.T) {
		got := restartRequiredKeys(goldenConfig, otherConfig)
		slices.Sort(got)
		// 25 restart-required dbKeys from the design block.
		want := []string{
			"cache_cleanup_interval", "cache_max_entry_size", "cache_max_size",
			"cache_max_time", "db_max_pool_size", "db_min_idle_connections",
			"db_optimize_interval", "db_pool_monitor_interval",
			"enable_http_cache", "enable_pprof", "image_directory",
			"listener_address", "listener_port", "log_directory", "log_level",
			"log_retention_count", "log_rollover", "queue_size",
			"session_http_only", "session_max_age",
			"session_same_site", "session_secure", "worker_pool_max",
			"worker_pool_max_idle_time", "worker_pool_min_idle",
		}
		slices.Sort(want)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("restartRequiredKeys mismatch\ngot:  %v\nwant: %v", got, want)
		}
	})

	t.Run("MergeDefaults", func(t *testing.T) {
		zeroConfig.MergeDefaults(DefaultConfig())
		def := DefaultConfig()

		// Every non-bool field should equal its default.
		if zeroConfig.ListenerAddress != def.ListenerAddress {
			t.Errorf("ListenerAddress = %q, want %q", zeroConfig.ListenerAddress, def.ListenerAddress)
		}
		if zeroConfig.ListenerPort != def.ListenerPort {
			t.Errorf("ListenerPort = %d, want %d", zeroConfig.ListenerPort, def.ListenerPort)
		}
		if zeroConfig.LogDirectory != def.LogDirectory {
			t.Errorf("LogDirectory = %q, want %q", zeroConfig.LogDirectory, def.LogDirectory)
		}
		if zeroConfig.LogLevel != def.LogLevel {
			t.Errorf("LogLevel = %q, want %q", zeroConfig.LogLevel, def.LogLevel)
		}
		if zeroConfig.LogRollover != def.LogRollover {
			t.Errorf("LogRollover = %q, want %q", zeroConfig.LogRollover, def.LogRollover)
		}
		if zeroConfig.LogRetentionCount != def.LogRetentionCount {
			t.Errorf("LogRetentionCount = %d, want %d", zeroConfig.LogRetentionCount, def.LogRetentionCount)
		}
		if zeroConfig.SiteName != def.SiteName {
			t.Errorf("SiteName = %q, want %q", zeroConfig.SiteName, def.SiteName)
		}
		if !reflect.DeepEqual(zeroConfig.Themes, def.Themes) {
			t.Errorf("Themes = %v, want %v", zeroConfig.Themes, def.Themes)
		}
		if zeroConfig.CurrentTheme != def.CurrentTheme {
			t.Errorf("CurrentTheme = %q, want %q", zeroConfig.CurrentTheme, def.CurrentTheme)
		}
		if zeroConfig.ImageDirectory != def.ImageDirectory {
			t.Errorf("ImageDirectory = %q, want %q", zeroConfig.ImageDirectory, def.ImageDirectory)
		}
		if zeroConfig.ETagVersion != def.ETagVersion {
			t.Errorf("ETagVersion = %q, want %q", zeroConfig.ETagVersion, def.ETagVersion)
		}
		if zeroConfig.SessionMaxAge != def.SessionMaxAge {
			t.Errorf("SessionMaxAge = %d, want %d", zeroConfig.SessionMaxAge, def.SessionMaxAge)
		}
		if zeroConfig.SessionSameSite != def.SessionSameSite {
			t.Errorf("SessionSameSite = %q, want %q", zeroConfig.SessionSameSite, def.SessionSameSite)
		}
		if zeroConfig.CacheMaxSize != def.CacheMaxSize {
			t.Errorf("CacheMaxSize = %d, want %d", zeroConfig.CacheMaxSize, def.CacheMaxSize)
		}
		if zeroConfig.CacheMaxTime != def.CacheMaxTime {
			t.Errorf("CacheMaxTime = %v, want %v", zeroConfig.CacheMaxTime, def.CacheMaxTime)
		}
		if zeroConfig.CacheMaxEntrySize != def.CacheMaxEntrySize {
			t.Errorf("CacheMaxEntrySize = %d, want %d", zeroConfig.CacheMaxEntrySize, def.CacheMaxEntrySize)
		}
		if zeroConfig.CacheCleanupInterval != def.CacheCleanupInterval {
			t.Errorf("CacheCleanupInterval = %v, want %v", zeroConfig.CacheCleanupInterval, def.CacheCleanupInterval)
		}
		if zeroConfig.DBMaxPoolSize != def.DBMaxPoolSize {
			t.Errorf("DBMaxPoolSize = %d, want %d", zeroConfig.DBMaxPoolSize, def.DBMaxPoolSize)
		}
		if zeroConfig.DBMinIdleConnections != def.DBMinIdleConnections {
			t.Errorf("DBMinIdleConnections = %d, want %d", zeroConfig.DBMinIdleConnections, def.DBMinIdleConnections)
		}
		if zeroConfig.DBOptimizeInterval != def.DBOptimizeInterval {
			t.Errorf("DBOptimizeInterval = %v, want %v", zeroConfig.DBOptimizeInterval, def.DBOptimizeInterval)
		}
		if zeroConfig.WorkerPoolMax != def.WorkerPoolMax {
			t.Errorf("WorkerPoolMax = %d, want %d", zeroConfig.WorkerPoolMax, def.WorkerPoolMax)
		}
		if zeroConfig.WorkerPoolMinIdle != def.WorkerPoolMinIdle {
			t.Errorf("WorkerPoolMinIdle = %d, want %d", zeroConfig.WorkerPoolMinIdle, def.WorkerPoolMinIdle)
		}
		if zeroConfig.WorkerPoolMaxIdleTime != def.WorkerPoolMaxIdleTime {
			t.Errorf("WorkerPoolMaxIdleTime = %v, want %v", zeroConfig.WorkerPoolMaxIdleTime, def.WorkerPoolMaxIdleTime)
		}
		if zeroConfig.DBPoolMonitorInterval != def.DBPoolMonitorInterval {
			t.Errorf("DBPoolMonitorInterval = %v, want %v", zeroConfig.DBPoolMonitorInterval, def.DBPoolMonitorInterval)
		}
		if zeroConfig.QueueSize != def.QueueSize {
			t.Errorf("QueueSize = %d, want %d", zeroConfig.QueueSize, def.QueueSize)
		}
		if zeroConfig.MaxHTTPCacheEntryInsertPerTransaction != def.MaxHTTPCacheEntryInsertPerTransaction {
			t.Errorf("MaxHTTPCacheEntryInsertPerTransaction = %d, want %d", zeroConfig.MaxHTTPCacheEntryInsertPerTransaction, def.MaxHTTPCacheEntryInsertPerTransaction)
		}
		if zeroConfig.HTTPCacheBodyCodec != def.HTTPCacheBodyCodec {
			t.Errorf("HTTPCacheBodyCodec = %q, want %q", zeroConfig.HTTPCacheBodyCodec, def.HTTPCacheBodyCodec)
		}
		if zeroConfig.LockoutDuration != def.LockoutDuration {
			t.Errorf("LockoutDuration = %d, want %d", zeroConfig.LockoutDuration, def.LockoutDuration)
		}
		if zeroConfig.LockoutThreshold != def.LockoutThreshold {
			t.Errorf("LockoutThreshold = %d, want %d", zeroConfig.LockoutThreshold, def.LockoutThreshold)
		}
		if zeroConfig.LoginRateLimitPerIP != def.LoginRateLimitPerIP {
			t.Errorf("LoginRateLimitPerIP = %d, want %d", zeroConfig.LoginRateLimitPerIP, def.LoginRateLimitPerIP)
		}

		// Bool fields must remain false (MergeDefaults should NOT overwrite explicit false).
		if zeroConfig.SessionHttpOnly != false {
			t.Errorf("SessionHttpOnly = %v, want false", zeroConfig.SessionHttpOnly)
		}
		if zeroConfig.SessionSecure != false {
			t.Errorf("SessionSecure = %v, want false", zeroConfig.SessionSecure)
		}
		if zeroConfig.EnableHTTPCache != false {
			t.Errorf("EnableHTTPCache = %v, want false", zeroConfig.EnableHTTPCache)
		}
		if zeroConfig.EnableCachePreload != false {
			t.Errorf("EnableCachePreload = %v, want false", zeroConfig.EnableCachePreload)
		}
		if zeroConfig.RunFileDiscovery != false {
			t.Errorf("RunFileDiscovery = %v, want false", zeroConfig.RunFileDiscovery)
		}
		if zeroConfig.EnablePprof != false {
			t.Errorf("EnablePprof = %v, want false", zeroConfig.EnablePprof)
		}
		if zeroConfig.DiscoveryQueueMax != 0 {
			t.Errorf("DiscoveryQueueMax = %d, want 0", zeroConfig.DiscoveryQueueMax)
		}
	})

	t.Run("RecoverFromCorruption", func(t *testing.T) {
		zeroConfig := &Config{}
		def := DefaultConfig()
		zeroConfig.RecoverFromCorruption(def)

		// Every scalar field should equal its default.
		if zeroConfig.ListenerAddress != def.ListenerAddress {
			t.Errorf("ListenerAddress = %q, want %q", zeroConfig.ListenerAddress, def.ListenerAddress)
		}
		if zeroConfig.ListenerPort != def.ListenerPort {
			t.Errorf("ListenerPort = %d, want %d", zeroConfig.ListenerPort, def.ListenerPort)
		}
		if zeroConfig.LogDirectory != def.LogDirectory {
			t.Errorf("LogDirectory = %q, want %q", zeroConfig.LogDirectory, def.LogDirectory)
		}
		if zeroConfig.LogLevel != def.LogLevel {
			t.Errorf("LogLevel = %q, want %q", zeroConfig.LogLevel, def.LogLevel)
		}
		if zeroConfig.LogRollover != def.LogRollover {
			t.Errorf("LogRollover = %q, want %q", zeroConfig.LogRollover, def.LogRollover)
		}
		if zeroConfig.LogRetentionCount != def.LogRetentionCount {
			t.Errorf("LogRetentionCount = %d, want %d", zeroConfig.LogRetentionCount, def.LogRetentionCount)
		}
		if zeroConfig.SiteName != def.SiteName {
			t.Errorf("SiteName = %q, want %q", zeroConfig.SiteName, def.SiteName)
		}
		if !reflect.DeepEqual(zeroConfig.Themes, def.Themes) {
			t.Errorf("Themes = %v, want %v", zeroConfig.Themes, def.Themes)
		}
		if zeroConfig.CurrentTheme != def.CurrentTheme {
			t.Errorf("CurrentTheme = %q, want %q", zeroConfig.CurrentTheme, def.CurrentTheme)
		}
		if zeroConfig.ImageDirectory != def.ImageDirectory {
			t.Errorf("ImageDirectory = %q, want %q", zeroConfig.ImageDirectory, def.ImageDirectory)
		}
		if zeroConfig.ETagVersion != def.ETagVersion {
			t.Errorf("ETagVersion = %q, want %q", zeroConfig.ETagVersion, def.ETagVersion)
		}
		if zeroConfig.SessionMaxAge != def.SessionMaxAge {
			t.Errorf("SessionMaxAge = %d, want %d", zeroConfig.SessionMaxAge, def.SessionMaxAge)
		}
		if zeroConfig.SessionHttpOnly != def.SessionHttpOnly {
			t.Errorf("SessionHttpOnly = %v, want %v", zeroConfig.SessionHttpOnly, def.SessionHttpOnly)
		}
		if zeroConfig.SessionSecure != def.SessionSecure {
			t.Errorf("SessionSecure = %v, want %v", zeroConfig.SessionSecure, def.SessionSecure)
		}
		if zeroConfig.SessionSameSite != def.SessionSameSite {
			t.Errorf("SessionSameSite = %q, want %q", zeroConfig.SessionSameSite, def.SessionSameSite)
		}
		if zeroConfig.EnableHTTPCache != def.EnableHTTPCache {
			t.Errorf("EnableHTTPCache = %v, want %v", zeroConfig.EnableHTTPCache, def.EnableHTTPCache)
		}
		if zeroConfig.CacheMaxSize != def.CacheMaxSize {
			t.Errorf("CacheMaxSize = %d, want %d", zeroConfig.CacheMaxSize, def.CacheMaxSize)
		}
		if zeroConfig.CacheMaxTime != def.CacheMaxTime {
			t.Errorf("CacheMaxTime = %v, want %v", zeroConfig.CacheMaxTime, def.CacheMaxTime)
		}
		if zeroConfig.CacheMaxEntrySize != def.CacheMaxEntrySize {
			t.Errorf("CacheMaxEntrySize = %d, want %d", zeroConfig.CacheMaxEntrySize, def.CacheMaxEntrySize)
		}
		if zeroConfig.CacheCleanupInterval != def.CacheCleanupInterval {
			t.Errorf("CacheCleanupInterval = %v, want %v", zeroConfig.CacheCleanupInterval, def.CacheCleanupInterval)
		}
		if zeroConfig.DBMaxPoolSize != def.DBMaxPoolSize {
			t.Errorf("DBMaxPoolSize = %d, want %d", zeroConfig.DBMaxPoolSize, def.DBMaxPoolSize)
		}
		if zeroConfig.DBMinIdleConnections != def.DBMinIdleConnections {
			t.Errorf("DBMinIdleConnections = %d, want %d", zeroConfig.DBMinIdleConnections, def.DBMinIdleConnections)
		}
		if zeroConfig.DBOptimizeInterval != def.DBOptimizeInterval {
			t.Errorf("DBOptimizeInterval = %v, want %v", zeroConfig.DBOptimizeInterval, def.DBOptimizeInterval)
		}
		if zeroConfig.WorkerPoolMax != def.WorkerPoolMax {
			t.Errorf("WorkerPoolMax = %d, want %d", zeroConfig.WorkerPoolMax, def.WorkerPoolMax)
		}
		if zeroConfig.WorkerPoolMinIdle != def.WorkerPoolMinIdle {
			t.Errorf("WorkerPoolMinIdle = %d, want %d", zeroConfig.WorkerPoolMinIdle, def.WorkerPoolMinIdle)
		}
		if zeroConfig.WorkerPoolMaxIdleTime != def.WorkerPoolMaxIdleTime {
			t.Errorf("WorkerPoolMaxIdleTime = %v, want %v", zeroConfig.WorkerPoolMaxIdleTime, def.WorkerPoolMaxIdleTime)
		}
		if zeroConfig.DBPoolMonitorInterval != def.DBPoolMonitorInterval {
			t.Errorf("DBPoolMonitorInterval = %v, want %v", zeroConfig.DBPoolMonitorInterval, def.DBPoolMonitorInterval)
		}
		if zeroConfig.QueueSize != def.QueueSize {
			t.Errorf("QueueSize = %d, want %d", zeroConfig.QueueSize, def.QueueSize)
		}
		if zeroConfig.EnableCachePreload != def.EnableCachePreload {
			t.Errorf("EnableCachePreload = %v, want %v", zeroConfig.EnableCachePreload, def.EnableCachePreload)
		}
		if zeroConfig.MaxHTTPCacheEntryInsertPerTransaction != def.MaxHTTPCacheEntryInsertPerTransaction {
			t.Errorf("MaxHTTPCacheEntryInsertPerTransaction = %d, want %d", zeroConfig.MaxHTTPCacheEntryInsertPerTransaction, def.MaxHTTPCacheEntryInsertPerTransaction)
		}
		if zeroConfig.HTTPCacheBodyCodec != def.HTTPCacheBodyCodec {
			t.Errorf("HTTPCacheBodyCodec = %q, want %q", zeroConfig.HTTPCacheBodyCodec, def.HTTPCacheBodyCodec)
		}
		if zeroConfig.LockoutDuration != def.LockoutDuration {
			t.Errorf("LockoutDuration = %d, want %d", zeroConfig.LockoutDuration, def.LockoutDuration)
		}
		if zeroConfig.LockoutThreshold != def.LockoutThreshold {
			t.Errorf("LockoutThreshold = %d, want %d", zeroConfig.LockoutThreshold, def.LockoutThreshold)
		}
		if zeroConfig.LoginRateLimitPerIP != def.LoginRateLimitPerIP {
			t.Errorf("LoginRateLimitPerIP = %d, want %d", zeroConfig.LoginRateLimitPerIP, def.LoginRateLimitPerIP)
		}
		if zeroConfig.RunFileDiscovery != def.RunFileDiscovery {
			t.Errorf("RunFileDiscovery = %v, want %v", zeroConfig.RunFileDiscovery, def.RunFileDiscovery)
		}
		if zeroConfig.EnablePprof != def.EnablePprof {
			t.Errorf("EnablePprof = %v, want %v", zeroConfig.EnablePprof, def.EnablePprof)
		}
		if zeroConfig.DiscoveryQueueMax != def.DiscoveryQueueMax {
			t.Errorf("DiscoveryQueueMax = %d, want %d", zeroConfig.DiscoveryQueueMax, def.DiscoveryQueueMax)
		}
	})
}
