package config

import (
	"testing"

	"github.com/lbe/sfpg-go/internal/getopt"
)

// TestConfig_LoadFromOptExcluding verifies LoadFromOptExcluding behavior.
func TestConfig_LoadFromOptExcluding(t *testing.T) {
	tests := []struct {
		name     string
		initial  Config
		opt      getopt.Opt
		exclude  []string
		expected Config
	}{
		{
			name: "applies all values when no exclusions",
			initial: Config{
				ListenerPort: 8080,
			},
			opt: getopt.Opt{
				Port:               getopt.OptInt{Int: 9090, IsSet: true},
				EnableCompression:  getopt.OptBool{Bool: true, IsSet: true},
				EnableHTTPCache:    getopt.OptBool{Bool: false, IsSet: true},
				EnableCachePreload: getopt.OptBool{Bool: true, IsSet: true},
				RunFileDiscovery:   getopt.OptBool{Bool: true, IsSet: true},
				SessionSecure:      getopt.OptBool{Bool: false, IsSet: true},
				SessionHttpOnly:    getopt.OptBool{Bool: false, IsSet: true},
				SessionMaxAge:      getopt.OptInt{Int: 3600, IsSet: true},
				SessionSameSite:    getopt.OptString{String: "Strict", IsSet: true},
			},
			exclude: []string{},
			expected: Config{
				ListenerPort:            9090,
				ServerCompressionEnable: true,
				EnableHTTPCache:         false,
				EnableCachePreload:      true,
				RunFileDiscovery:        true,
				SessionSecure:           false,
				SessionHttpOnly:         false,
				SessionMaxAge:           3600,
				SessionSameSite:         "Strict",
			},
		},
		{
			name: "respects excluded listener_port",
			initial: Config{
				ListenerPort: 8080,
			},
			opt: getopt.Opt{
				Port: getopt.OptInt{Int: 9090, IsSet: true},
			},
			exclude: []string{"listener_port"},
			expected: Config{
				ListenerPort: 8080, // unchanged
			},
		},
		{
			name: "respects excluded enable_http_cache",
			initial: Config{
				EnableHTTPCache: true,
			},
			opt: getopt.Opt{
				EnableHTTPCache: getopt.OptBool{Bool: false, IsSet: true},
			},
			exclude: []string{"enable_http_cache"},
			expected: Config{
				EnableHTTPCache: true, // unchanged
			},
		},
		{
			name: "applies non-excluded values when some are excluded",
			initial: Config{
				ListenerPort:            8080,
				ServerCompressionEnable: false,
				EnableHTTPCache:         true,
			},
			opt: getopt.Opt{
				Port:              getopt.OptInt{Int: 9090, IsSet: true},
				EnableCompression: getopt.OptBool{Bool: true, IsSet: true},
				EnableHTTPCache:   getopt.OptBool{Bool: false, IsSet: true},
			},
			exclude: []string{"listener_port"},
			expected: Config{
				ListenerPort:            8080,  // unchanged - excluded
				ServerCompressionEnable: true,  // changed - not excluded
				EnableHTTPCache:         false, // changed - not excluded
			},
		},
		{
			name: "does not apply unset values",
			initial: Config{
				ListenerPort: 8080,
			},
			opt: getopt.Opt{
				Port: getopt.OptInt{Int: 9090, IsSet: false}, // not set
			},
			exclude: []string{},
			expected: Config{
				ListenerPort: 8080, // unchanged
			},
		},
		{
			name: "respects multiple excluded fields",
			initial: Config{
				ListenerPort:            8080,
				EnableHTTPCache:         true,
				ServerCompressionEnable: false,
			},
			opt: getopt.Opt{
				Port:              getopt.OptInt{Int: 9090, IsSet: true},
				EnableCompression: getopt.OptBool{Bool: true, IsSet: true},
				EnableHTTPCache:   getopt.OptBool{Bool: false, IsSet: true},
			},
			exclude: []string{"listener_port", "enable_http_cache"},
			expected: Config{
				ListenerPort:            8080, // unchanged - excluded
				EnableHTTPCache:         true, // unchanged - excluded
				ServerCompressionEnable: true, // changed - not excluded
			},
		},
		{
			name: "handles session options exclusions",
			initial: Config{
				SessionSecure:   true,
				SessionHttpOnly: true,
				SessionMaxAge:   7200,
				SessionSameSite: "Lax",
			},
			opt: getopt.Opt{
				SessionSecure:   getopt.OptBool{Bool: false, IsSet: true},
				SessionHttpOnly: getopt.OptBool{Bool: false, IsSet: true},
				SessionMaxAge:   getopt.OptInt{Int: 3600, IsSet: true},
				SessionSameSite: getopt.OptString{String: "Strict", IsSet: true},
			},
			exclude: []string{"session_secure", "session_max_age"},
			expected: Config{
				SessionSecure:   true,     // unchanged - excluded
				SessionHttpOnly: false,    // changed - not excluded
				SessionMaxAge:   7200,     // unchanged - excluded
				SessionSameSite: "Strict", // changed - not excluded
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.initial
			cfg.LoadFromOptExcluding(tt.opt, tt.exclude)

			if cfg.ListenerPort != tt.expected.ListenerPort {
				t.Errorf("ListenerPort: got %d, want %d", cfg.ListenerPort, tt.expected.ListenerPort)
			}
			if cfg.ServerCompressionEnable != tt.expected.ServerCompressionEnable {
				t.Errorf("ServerCompressionEnable: got %v, want %v", cfg.ServerCompressionEnable, tt.expected.ServerCompressionEnable)
			}
			if cfg.EnableHTTPCache != tt.expected.EnableHTTPCache {
				t.Errorf("EnableHTTPCache: got %v, want %v", cfg.EnableHTTPCache, tt.expected.EnableHTTPCache)
			}
			if cfg.EnableCachePreload != tt.expected.EnableCachePreload {
				t.Errorf("EnableCachePreload: got %v, want %v", cfg.EnableCachePreload, tt.expected.EnableCachePreload)
			}
			if cfg.RunFileDiscovery != tt.expected.RunFileDiscovery {
				t.Errorf("RunFileDiscovery: got %v, want %v", cfg.RunFileDiscovery, tt.expected.RunFileDiscovery)
			}
			if cfg.SessionSecure != tt.expected.SessionSecure {
				t.Errorf("SessionSecure: got %v, want %v", cfg.SessionSecure, tt.expected.SessionSecure)
			}
			if cfg.SessionHttpOnly != tt.expected.SessionHttpOnly {
				t.Errorf("SessionHttpOnly: got %v, want %v", cfg.SessionHttpOnly, tt.expected.SessionHttpOnly)
			}
			if cfg.SessionMaxAge != tt.expected.SessionMaxAge {
				t.Errorf("SessionMaxAge: got %d, want %d", cfg.SessionMaxAge, tt.expected.SessionMaxAge)
			}
			if cfg.SessionSameSite != tt.expected.SessionSameSite {
				t.Errorf("SessionSameSite: got %q, want %q", cfg.SessionSameSite, tt.expected.SessionSameSite)
			}
		})
	}
}

// TestConfig_LoadFromOptExcluding_EmptyExcludeList verifies behavior with nil/empty exclude.
func TestConfig_LoadFromOptExcluding_EmptyExcludeList(t *testing.T) {
	cfg := Config{
		ListenerPort: 8080,
	}

	opt := getopt.Opt{
		Port: getopt.OptInt{Int: 9090, IsSet: true},
	}

	// Nil exclude list should behave like empty list
	cfg.LoadFromOptExcluding(opt, nil)
	if cfg.ListenerPort != 9090 {
		t.Errorf("ListenerPort: got %d, want 9090", cfg.ListenerPort)
	}

	// Reset and test with empty list
	cfg.ListenerPort = 8080
	cfg.LoadFromOptExcluding(opt, []string{})
	if cfg.ListenerPort != 9090 {
		t.Errorf("ListenerPort: got %d, want 9090", cfg.ListenerPort)
	}
}
