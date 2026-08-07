package security

import (
	"net"
	"sync"
	"time"
)

// Default IP rate limiting values.
const (
	DefaultLoginRateLimitPerIP = 10 // max login attempts per IP per window
	DefaultRateLimitWindow     = 60 // window in seconds (1 minute)
	defaultGCInterval          = 64 // sweep idle IPs every N Allow calls (when limited)
)

// IPRateLimiter implements a per-IP sliding window rate limiter.
// It is safe for concurrent use.
type IPRateLimiter struct {
	mu         sync.RWMutex
	attempts   map[string][]int64 // IP -> sorted timestamps
	max        int                // max attempts in window
	window     int64              // window in seconds
	allowCount uint64             // Allow calls while limited (drives periodic GC)
	gcInterval int                // sweep stale map keys every N Allow calls
}

// EffectiveLoginRateLimitPerIP converts the configured login rate limit into
// the effective per-IP maximum. A config value <= 0 means unlimited (0).
func EffectiveLoginRateLimitPerIP(cfgMax int) int {
	if cfgMax > 0 {
		return cfgMax
	}
	return 0 // unlimited
}

// NewIPRateLimiter creates a new IPRateLimiter.
// If max <= 0, rate limiting is disabled (unlimited).
// If windowSec <= 0, DefaultRateLimitWindow is used.
func NewIPRateLimiter(max int, windowSec int64) *IPRateLimiter {
	if windowSec <= 0 {
		windowSec = DefaultRateLimitWindow
	}
	return &IPRateLimiter{
		attempts:   make(map[string][]int64),
		max:        max,
		window:     windowSec,
		gcInterval: defaultGCInterval,
	}
}

// Allow checks if the given IP is allowed to proceed.
// It records the attempt and returns true if within the limit.
// Returns false if the IP has exceeded the rate limit.
// If rl.max <= 0, rate limiting is disabled and always returns true.
func (rl *IPRateLimiter) Allow(ip string) bool {
	if rl.max <= 0 {
		return true // unlimited
	}

	now := time.Now().Unix()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := now - rl.window
	valid := pruneAttempts(rl.attempts[ip], cutoff)

	if len(valid) >= rl.max {
		if len(valid) == 0 {
			delete(rl.attempts, ip)
		} else {
			rl.attempts[ip] = valid
		}
		rl.maybeGCStaleLocked(now)
		return false
	}

	valid = append(valid, now)
	rl.attempts[ip] = valid
	rl.maybeGCStaleLocked(now)
	return true
}

func pruneAttempts(attempts []int64, cutoff int64) []int64 {
	var valid []int64
	for _, t := range attempts {
		if t > cutoff {
			valid = append(valid, t)
		}
	}
	return valid
}

// maybeGCStaleLocked removes map entries whose timestamps are all outside the
// sliding window. Caller must hold rl.mu.
func (rl *IPRateLimiter) maybeGCStaleLocked(now int64) {
	rl.allowCount++
	if rl.gcInterval <= 0 || rl.allowCount%uint64(rl.gcInterval) != 0 {
		return
	}
	cutoff := now - rl.window
	for ip, attempts := range rl.attempts {
		if len(pruneAttempts(attempts, cutoff)) == 0 {
			delete(rl.attempts, ip)
		}
	}
}

// trackedIPCount returns the number of IPs with stored attempt history.
// It is intended for tests in this package.
func (rl *IPRateLimiter) trackedIPCount() int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return len(rl.attempts)
}

// SetMax updates the per-IP attempt cap without resetting in-window history.
// If max <= 0, subsequent Allow calls are unlimited until max is raised again.
func (rl *IPRateLimiter) SetMax(max int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.max = max
}

// Clear drops all recorded attempts (starts a fresh window).
func (rl *IPRateLimiter) Clear() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.attempts = make(map[string][]int64)
	rl.allowCount = 0
}

// RateLimitFromRequestKey extracts the client IP from a request's RemoteAddr
// (host portion of "host:port"). Uses the direct connection address only;
// X-Forwarded-For is intentionally not consulted (see config modal help text).
func RateLimitFromRequestKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}
