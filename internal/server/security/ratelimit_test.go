package security

import (
	"testing"
	"time"
)

func TestNewIPRateLimiter(t *testing.T) {
	rl := NewIPRateLimiter(5, 60)
	if rl == nil {
		t.Fatal("NewIPRateLimiter returned nil")
	}
	if rl.max != 5 {
		t.Errorf("max = %d, want 5", rl.max)
	}
	if rl.window != 60 {
		t.Errorf("window = %d, want 60", rl.window)
	}
}

func TestNewIPRateLimiter_Defaults(t *testing.T) {
	rl := NewIPRateLimiter(0, 0)
	if rl.max != 0 {
		t.Errorf("max = %d, want 0 (unlimited)", rl.max)
	}
	if rl.window != DefaultRateLimitWindow {
		t.Errorf("window = %d, want %d", rl.window, DefaultRateLimitWindow)
	}
}

func TestNewIPRateLimiter_Negative(t *testing.T) {
	rl := NewIPRateLimiter(-1, -1)
	if rl.max != -1 {
		t.Errorf("max = %d, want -1 (unlimited)", rl.max)
	}
	if rl.window != DefaultRateLimitWindow {
		t.Errorf("window = %d, want %d", rl.window, DefaultRateLimitWindow)
	}
}

func TestIPRateLimiter_Unlimited(t *testing.T) {
	rl := NewIPRateLimiter(0, 60)
	ip := "192.168.1.1"

	// Any number of attempts should always be allowed.
	for i := range 100 {
		if !rl.Allow(ip) {
			t.Errorf("attempt %d: expected allowed (unlimited), got denied", i+1)
		}
	}
}

func TestIPRateLimiter_Allow(t *testing.T) {
	// Allow up to 3 per 60 second window.
	rl := NewIPRateLimiter(3, 60)
	ip := "192.168.1.1"

	// First 3 attempts should be allowed.
	for i := range 3 {
		if !rl.Allow(ip) {
			t.Errorf("attempt %d: expected allowed, got denied", i+1)
		}
	}

	// 4th attempt should be denied.
	if rl.Allow(ip) {
		t.Error("attempt 4: expected denied, got allowed")
	}
}

func TestIPRateLimiter_DifferentIPs(t *testing.T) {
	rl := NewIPRateLimiter(2, 60)

	// Allow 2 from each IP.
	for i := range 2 {
		if !rl.Allow("192.168.1.1") {
			t.Errorf("192.168.1.1 attempt %d: expected allowed", i+1)
		}
		if !rl.Allow("10.0.0.1") {
			t.Errorf("10.0.0.1 attempt %d: expected allowed", i+1)
		}
	}

	// 3rd from each should be denied.
	if rl.Allow("192.168.1.1") {
		t.Error("192.168.1.1 attempt 3: expected denied")
	}
	if rl.Allow("10.0.0.1") {
		t.Error("10.0.0.1 attempt 3: expected denied")
	}
}

func TestIPRateLimiter_WindowExpiry(t *testing.T) {
	// Use a short 1-second window.
	rl := NewIPRateLimiter(1, 1)
	ip := "192.168.1.1"

	if !rl.Allow(ip) {
		t.Fatal("attempt 1: expected allowed")
	}

	// Immediately should be denied.
	if rl.Allow(ip) {
		t.Error("attempt 2 (immediate): expected denied")
	}

	// Wait for window to expire.
	time.Sleep(1100 * time.Millisecond)

	if !rl.Allow(ip) {
		t.Error("attempt after window expiry: expected allowed")
	}
}

func TestRateLimitFromRequestKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"192.168.1.1:12345", "192.168.1.1"},
		{"10.0.0.1:80", "10.0.0.1"},
		{"[::1]:8080", "::1"},
		{"192.168.1.1", "192.168.1.1"}, // no port
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := RateLimitFromRequestKey(tt.input)
			if got != tt.expected {
				t.Errorf("RateLimitFromRequestKey(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestIPRateLimiter_InteractsWithLockout(t *testing.T) {
	// Verify that an IP rate limit and account lockout are independent layers.
	// A single IP may try many usernames (caught by IP rate limiter).
	// A single account may be locked from many IPs (caught by DB lockout).
	rl := NewIPRateLimiter(2, 60)

	// Exhaust IP rate limit for one IP.
	rl.Allow("10.0.0.1")
	rl.Allow("10.0.0.1")

	// Same IP blocked regardless of target account.
	if rl.Allow("10.0.0.1") {
		t.Error("expected IP blocked after limit")
	}

	// Different IP still allowed.
	if !rl.Allow("10.0.0.2") {
		t.Error("different IP should still be allowed")
	}
}

func TestEffectiveLoginRateLimitPerIP(t *testing.T) {
	tests := []struct {
		name   string
		cfgMax int
		want   int
	}{
		{name: "zero means unlimited", cfgMax: 0, want: 0},
		{name: "negative means unlimited", cfgMax: -1, want: 0},
		{name: "positive passes through", cfgMax: 10, want: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EffectiveLoginRateLimitPerIP(tt.cfgMax); got != tt.want {
				t.Errorf("EffectiveLoginRateLimitPerIP(%d) = %d, want %d", tt.cfgMax, got, tt.want)
			}
		})
	}
}

func TestIPRateLimiter_SetMax(t *testing.T) {
	ip := "1.2.3.4"

	// Part A — raising max (existing history kept — SetMax alone does NOT clear).
	rl := NewIPRateLimiter(5, 60)
	for i := range 5 {
		if !rl.Allow(ip) {
			t.Fatalf("attempt %d: expected allowed, got denied", i+1)
		}
	}
	rl.SetMax(10)
	if !rl.Allow(ip) {
		t.Error("after SetMax(10): sixth attempt expected allowed (raised cap; five prior attempts still in window)")
	}

	// Part B — lowering max (fresh limiter so history does not confuse expectations).
	rl2 := NewIPRateLimiter(5, 60)
	rl2.SetMax(1)
	if !rl2.Allow(ip) {
		t.Error("rl2 attempt 1: expected allowed")
	}
	if rl2.Allow(ip) {
		t.Error("rl2 attempt 2: expected denied")
	}

	// Part C — disable via 0.
	rl2.SetMax(0)
	if !rl2.Allow(ip) {
		t.Error("after SetMax(0): expected allowed (unlimited)")
	}
}

func TestIPRateLimiter_Clear(t *testing.T) {
	rl := NewIPRateLimiter(1, 60)
	ip := "192.168.1.1"

	if !rl.Allow(ip) {
		t.Fatal("attempt 1: expected allowed")
	}
	if rl.Allow(ip) {
		t.Error("attempt 2: expected denied")
	}

	rl.Clear()

	if !rl.Allow(ip) {
		t.Error("after Clear: expected allowed")
	}
}

func TestIPRateLimiter_GCRemovesIdleIPs(t *testing.T) {
	rl := NewIPRateLimiter(3, 1)
	rl.gcInterval = 1 // sweep on every Allow while testing

	if !rl.Allow("10.0.0.1") {
		t.Fatal("10.0.0.1: expected allowed")
	}
	if !rl.Allow("10.0.0.2") {
		t.Fatal("10.0.0.2: expected allowed")
	}
	if rl.trackedIPCount() != 2 {
		t.Fatalf("tracked IPs = %d, want 2", rl.trackedIPCount())
	}

	time.Sleep(1100 * time.Millisecond)

	if !rl.Allow("10.0.0.3") {
		t.Fatal("10.0.0.3: expected allowed after idle window")
	}
	if got := rl.trackedIPCount(); got != 1 {
		t.Fatalf("tracked IPs after GC = %d, want 1 (only active IP)", got)
	}
}

func TestIPRateLimiter_GCKeepsLimitedIP(t *testing.T) {
	rl := NewIPRateLimiter(1, 60)
	rl.gcInterval = 1

	if !rl.Allow("10.0.0.1") {
		t.Fatal("expected allowed")
	}
	if rl.Allow("10.0.0.1") {
		t.Fatal("expected denied")
	}
	if rl.trackedIPCount() != 1 {
		t.Fatalf("tracked IPs = %d, want 1 while still in window", rl.trackedIPCount())
	}
}
