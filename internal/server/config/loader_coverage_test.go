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
				ListenerPort:       9090,
				EnableHTTPCache:    false,
				EnableCachePreload: true,
				RunFileDiscovery:   true,
				SessionSecure:      false,
				SessionHttpOnly:    false,
				SessionMaxAge:      3600,
				SessionSameSite:    "Strict",
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
				ListenerPort:    8080,
				EnableHTTPCache: true,
			},
			opt: getopt.Opt{
				Port:            getopt.OptInt{Int: 9090, IsSet: true},
				EnableHTTPCache: getopt.OptBool{Bool: false, IsSet: true},
			},
			exclude: []string{"listener_port"},
			expected: Config{
				ListenerPort:    8080,  // unchanged - excluded
				EnableHTTPCache: false, // changed - not excluded
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
				ListenerPort:    8080,
				EnableHTTPCache: true,
			},
			opt: getopt.Opt{
				Port:            getopt.OptInt{Int: 9090, IsSet: true},
				EnableHTTPCache: getopt.OptBool{Bool: false, IsSet: true},
			},
			exclude: []string{"listener_port", "enable_http_cache"},
			expected: Config{
				ListenerPort:    8080, // unchanged - excluded
				EnableHTTPCache: true, // unchanged - excluded
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

// TestLoadFromOptDBKeysExistInFields verifies every cliRoutes dbKey exists in the
// fields() registry. This prevents the maintenance hazard where a field is added
// to fields() or cliRoutes but not the other.
func TestLoadFromOptDBKeysExistInFields(t *testing.T) {
	fieldMap := make(map[string]bool, len(fields()))
	for _, f := range fields() {
		fieldMap[f.dbKey] = true
	}
	for _, r := range cliRoutes {
		if !fieldMap[r.dbKey] {
			t.Errorf("cliRoutes dbKey %q not found in fields() — add it to fields() or fix the dbKey", r.dbKey)
		}
	}
	// Also check for unused dbKeys in fields() — fields with a CLI counterpart.
	// This is informational only; many fields intentionally have no CLI flag.
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

// TestYAMLValueToSetString covers conversion of YAML-decoded values to strings.
func TestYAMLValueToSetString(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    string
		wantErr bool
	}{
		{"nil", nil, "", true},
		{"unsupported map type", map[string]interface{}{"a": "b"}, "", true},
		{"string", "dark", "dark", false},
		{"int", 42, "42", false},
		{"bool", true, "true", false},
		{"sequence", []interface{}{"dark", "light"}, `["dark","light"]`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := yamlValueToSetString(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("yamlValueToSetString(%#v) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("yamlValueToSetString(%#v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
