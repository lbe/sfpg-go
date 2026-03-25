package config

import (
	"os"
	"testing"
	"time"
)

// TestParseDefaults uses default values when no flags or env vars set
func TestParseDefaults(t *testing.T) {
	// Clear environment
	os.Unsetenv("SFPG_SERVER")
	os.Unsetenv("SFPG_USERNAME")
	os.Unsetenv("SFPG_PASSWORD")

	cfg := ParseArgs([]string{})

	if cfg.ServerURL != "http://localhost:8083" {
		t.Errorf("ServerURL = %q, want %q", cfg.ServerURL, "http://localhost:8083")
	}
	if cfg.Refresh != 5*time.Second {
		t.Errorf("Refresh = %v, want %v", cfg.Refresh, 5*time.Second)
	}
	if cfg.NoRefresh {
		t.Error("NoRefresh should be false by default")
	}
}

// TestParseEnvironment reads credentials from environment
func TestParseEnvironment(t *testing.T) {
	os.Setenv("SFPG_SERVER", "http://example.com:8080")
	os.Setenv("SFPG_USERNAME", "testuser")
	os.Setenv("SFPG_PASSWORD", "testpass")
	defer func() {
		os.Unsetenv("SFPG_SERVER")
		os.Unsetenv("SFPG_USERNAME")
		os.Unsetenv("SFPG_PASSWORD")
	}()

	cfg := ParseArgs([]string{})

	if cfg.ServerURL != "http://example.com:8080" {
		t.Errorf("ServerURL = %q, want %q", cfg.ServerURL, "http://example.com:8080")
	}
	if cfg.Username != "testuser" {
		t.Errorf("Username = %q, want %q", cfg.Username, "testuser")
	}
	if cfg.Password != "testpass" {
		t.Errorf("Password = %q, want %q", cfg.Password, "testpass")
	}
}

// TestParseFlags reads values from command line flags
func TestParseFlags(t *testing.T) {
	os.Unsetenv("SFPG_SERVER")
	defer os.Unsetenv("SFPG_SERVER")

	cfg := ParseArgs([]string{"-server", "http://custom:9999", "-refresh", "10s", "-no-refresh"})

	if cfg.ServerURL != "http://custom:9999" {
		t.Errorf("ServerURL = %q, want %q", cfg.ServerURL, "http://custom:9999")
	}
	if cfg.Refresh != 10*time.Second {
		t.Errorf("Refresh = %v, want %v", cfg.Refresh, 10*time.Second)
	}
	if !cfg.NoRefresh {
		t.Error("NoRefresh should be true")
	}
}
