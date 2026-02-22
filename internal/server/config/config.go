package config

import (
	"os"
	"time"
)

// Config represents the complete application configuration.
// All settings are stored as strings in the database and converted as needed.
type Config struct {
	// Server settings (restart required)
	ListenerAddress string
	ListenerPort    int

	// Logging settings (restart required)
	LogDirectory      string
	LogLevel          string // "debug", "info", "warn", "error"
	LogRollover       string // "daily", "weekly", "monthly"
	LogRetentionCount int

	// Application settings (runtime)
	SiteName       string
	Themes         []string
	CurrentTheme   string
	ImageDirectory string

	// ETag version for cache busting (runtime changeable)
	ETagVersion string

	// Session settings (restart required)
	// These fields control session cookie security and CSRF protection.
	// Changes require server restart to take effect.

	// SessionMaxAge is the session lifetime in seconds.
	// Defines how long a session remains valid before requiring re-authentication.
	// Security implications: Shorter sessions (e.g., 1-7 days) reduce the window for
	// session hijacking attacks. Defaults to 7 days (604800 seconds).
	// Recommended: 1-7 days depending on use case.
	SessionMaxAge int

	// SessionHttpOnly prevents JavaScript access to session cookies.
	// When true, the HttpOnly flag is set on the session cookie, protecting against
	// Cross-Site Scripting (XSS) attacks by preventing malicious scripts from reading
	// or stealing the session token.
	// Defaults to true (recommended for production).
	// Security note: Always keep true unless specifically required for XSS-vulnerable
	// legacy applications.
	SessionHttpOnly bool

	// SessionSecure restricts session cookies to HTTPS connections only.
	// When true, the Secure flag is set on the session cookie, ensuring that
	// the cookie is only transmitted over encrypted HTTPS connections.
	// Prevents session hijacking via man-in-the-middle (MITM) attacks on unencrypted HTTP.
	// Defaults to true (required for production).
	// Security note: Must be true in production with HTTPS. Can be false only for
	// development over HTTP, but is dangerous in production.
	SessionSecure bool

	// SessionSameSite controls the SameSite cookie attribute for CSRF protection.
	// Valid values: "Lax", "Strict", or "None".
	//
	// - "Lax" (default, recommended): Cookies are sent with same-site requests and
	//   top-level navigations (safe for most applications). Provides strong CSRF
	//   protection while maintaining reasonable user experience.
	//
	// - "Strict": Cookies are sent only with same-site requests. Most restrictive,
	//   blocking cookies even on top-level navigations from external sites.
	//   Best for highly sensitive applications (e.g., banking), but may inconvenience
	//   users following external links to your site.
	//
	// - "None": Cookies are sent with all requests (cross-site included).
	//   Only use with Secure=true if cross-site requests require authentication.
	//   Essentially disables SameSite CSRF protection; use only with careful consideration.
	//
	// Security implications: SameSite is a modern defense against CSRF attacks.
	// Lax provides excellent protection without sacrificing usability. Combined with
	// explicit CSRF token validation on state-changing requests, provides
	// defense-in-depth against CSRF exploits.
	SessionSameSite string

	// Performance settings (restart required)
	ServerCompressionEnable bool
	EnableHTTPCache         bool
	CacheMaxSize            int64         // in bytes
	CacheMaxTime            time.Duration // TTL
	CacheMaxEntrySize       int64         // in bytes
	CacheCleanupInterval    time.Duration
	DBMaxPoolSize           int
	DBMinIdleConnections    int
	DBOptimizeInterval      time.Duration
	WorkerPoolMax           int // 0 means auto-calculate
	WorkerPoolMinIdle       int // 0 means auto-calculate
	WorkerPoolMaxIdleTime   time.Duration
	QueueSize               int
	EnableCachePreload      bool // Pre-load HTTP cache when folders are opened (runtime, no restart)
	// MaxHTTPCacheEntryInsertPerTransaction is the max number of cache entries to insert in one transaction (batch size). Default: 10.
	MaxHTTPCacheEntryInsertPerTransaction int

	// Discovery settings (runtime)
	RunFileDiscovery bool
}

// DefaultConfig returns a Config with all default values.
func DefaultConfig() *Config {
	today := time.Now().Format("20060102")
	return &Config{
		// Server
		ListenerAddress: "0.0.0.0",
		ListenerPort:    8081,

		// Logging
		LogDirectory:      "", // Will be set to {rootDir}/logs
		LogLevel:          "debug",
		LogRollover:       "weekly",
		LogRetentionCount: 7,

		// Application
		SiteName:       "SFPG Gallery",
		Themes:         []string{"dark", "light"},
		CurrentTheme:   "dark",
		ImageDirectory: "", // Will be set to {rootDir}/Images
		ETagVersion:    today + "-01",

		// Session
		SessionMaxAge:   7 * 24 * 3600, // 7 days in seconds
		SessionHttpOnly: true,
		SessionSecure:   true,
		SessionSameSite: "Lax",

		// Performance
		ServerCompressionEnable:               true,
		EnableHTTPCache:                       true,
		CacheMaxSize:                          500 * 1024 * 1024,   // 500MB
		CacheMaxTime:                          30 * 24 * time.Hour, // 30 days
		CacheMaxEntrySize:                     10 * 1024 * 1024,    // 10MB
		CacheCleanupInterval:                  5 * time.Minute,
		DBMaxPoolSize:                         100,
		DBMinIdleConnections:                  10,
		DBOptimizeInterval:                    1 * time.Hour,
		WorkerPoolMax:                         0, // Auto-calculate
		WorkerPoolMinIdle:                     0, // Auto-calculate
		WorkerPoolMaxIdleTime:                 10 * time.Second,
		QueueSize:                             10000,
		EnableCachePreload:                    true,
		MaxHTTPCacheEntryInsertPerTransaction: 10,

		// Discovery
		RunFileDiscovery: true,
	}
}

// FileExists checks if a path exists and is a readable file.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// RecoverFromCorruption restores a corrupted configuration by copying values from defaults.
// This is used when configuration is corrupted and needs to be restored to safe defaults.
func (c *Config) RecoverFromCorruption(defaults *Config) {
	if defaults == nil {
		return
	}

	// Restore all fields from defaults
	c.ListenerAddress = defaults.ListenerAddress
	c.ListenerPort = defaults.ListenerPort
	c.LogDirectory = defaults.LogDirectory
	c.LogLevel = defaults.LogLevel
	c.LogRollover = defaults.LogRollover
	c.LogRetentionCount = defaults.LogRetentionCount
	c.SiteName = defaults.SiteName
	c.Themes = make([]string, len(defaults.Themes))
	copy(c.Themes, defaults.Themes)
	c.CurrentTheme = defaults.CurrentTheme
	c.ImageDirectory = defaults.ImageDirectory
	c.ETagVersion = defaults.ETagVersion
	c.SessionMaxAge = defaults.SessionMaxAge
	c.SessionHttpOnly = defaults.SessionHttpOnly
	c.SessionSecure = defaults.SessionSecure
	c.SessionSameSite = defaults.SessionSameSite
	c.ServerCompressionEnable = defaults.ServerCompressionEnable
	c.EnableHTTPCache = defaults.EnableHTTPCache
	c.CacheMaxSize = defaults.CacheMaxSize
	c.CacheMaxTime = defaults.CacheMaxTime
	c.CacheMaxEntrySize = defaults.CacheMaxEntrySize
	c.CacheCleanupInterval = defaults.CacheCleanupInterval
	c.DBMaxPoolSize = defaults.DBMaxPoolSize
	c.DBMinIdleConnections = defaults.DBMinIdleConnections
	c.DBOptimizeInterval = defaults.DBOptimizeInterval
	c.WorkerPoolMax = defaults.WorkerPoolMax
	c.WorkerPoolMinIdle = defaults.WorkerPoolMinIdle
	c.WorkerPoolMaxIdleTime = defaults.WorkerPoolMaxIdleTime
	c.QueueSize = defaults.QueueSize
	c.EnableCachePreload = defaults.EnableCachePreload
	c.MaxHTTPCacheEntryInsertPerTransaction = defaults.MaxHTTPCacheEntryInsertPerTransaction
	c.RunFileDiscovery = defaults.RunFileDiscovery
}
