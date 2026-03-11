package getopt

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// OptInt holds an integer value and tracks whether it was explicitly set.
type OptInt struct {
	Int   int
	IsSet bool
}

// OptBool holds a boolean value and tracks whether it was explicitly set.
type OptBool struct {
	Bool  bool
	IsSet bool
}

// OptString holds a string value and tracks whether it was explicitly set.
type OptString struct {
	String string
	IsSet  bool
}

// Package getopt parses runtime configuration from environment variables and command-line flags.
// Precedence: CLI flags > Environment variables.
// Opt holds all runtime options parsed from env and CLI flags.
// All fields use nullable types to track whether they were explicitly set.
type Opt struct {
	Port                 OptInt    // TCP port the HTTP server binds to
	RunFileDiscovery     OptBool   // Whether to run discovery on startup
	DebugDelayMS         OptInt    // Optional artificial delay for debug/testing
	Profile              OptString // Profiling mode: "", "cpu", "mem", "block", etc.
	EnableCompression    OptBool   // Enable gzip/brotli compression
	EnableHTTPCache      OptBool   // Enable SQLite HTTP response caching
	EnableCachePreload   OptBool   // Enable cache preloading when folders are opened
	SessionSecret        OptString // Secret key for session cookie signing (required)
	SessionSecure        OptBool   // Restrict session cookies to HTTPS
	SessionHttpOnly      OptBool   // Set HttpOnly flag on session cookies
	SessionMaxAge        OptInt    // Max age for session cookies in seconds
	SessionSameSite      OptString // SameSite policy for session cookies (Strict, Lax, None)
	UnlockAccount        OptString // Username to unlock (empty string if not set)
	RestoreLastKnownGood OptBool   // Restore last known good configuration from database on startup
	IncrementETag        OptBool   // Increment application-wide ETag version on startup
	CacheBatchLoad       OptBool   // Run cache batch load and exit (CLI one-shot)
}

// defaultOpt returns an Opt with all zero values (no defaults).
// Values are only set when explicitly provided via environment variables or CLI flags.
func defaultOpt() Opt {
	return Opt{
		// All fields use zero values - no defaults
		// IsSet will be true only when explicitly set via env/CLI
	}
}

// Parse reads configuration from environment variables and CLI flags.
// Precedence: CLI flags > Environment variables.
// YAML config files are NOT handled here - they are handled by Config.LoadFromYAML().
func Parse() Opt {
	opt := defaultOpt()

	// Apply environment variables first (lower precedence)
	applyEnvVars(&opt)

	// Apply CLI flags (higher precedence, overrides env vars)
	if err := applyCLIFlags(&opt); err != nil {
		usageExit(err.Error())
	}

	// Validate that required fields are set
	if err := validateOpt(&opt); err != nil {
		usageExit(err.Error())
	}

	return opt
}

// ParseEnvOnly reads configuration from environment variables only (no CLI flags).
// Used by tests to parse env vars set with t.Setenv() without interfering with test flags.
func ParseEnvOnly() Opt {
	opt := defaultOpt()
	applyEnvVars(&opt)
	return opt
}

func parseBoolEnv(v string) (bool, error) {
	s := strings.TrimSpace(strings.ToLower(v))
	switch s {
	case "1", "true", "t", "yes", "y":
		return true, nil
	case "0", "false", "f", "no", "n":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value: %q", v)
	}
}

// usageExit is a hookable exit function for testing.
var usageExit = func(msg string) {
	if msg != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n\n", msg)
	}
	fmt.Fprintf(os.Stderr, "Usage:\n")
	flag.PrintDefaults()
	os.Exit(1)
}

// getUsageExit returns the current usageExit function for testing.
func getUsageExit() func(string) {
	return usageExit
}

// setUsageExit sets the usageExit function for testing.
func setUsageExit(fn func(string)) {
	usageExit = fn
}

// Phase 1.1: getExecutableDir returns the directory of the running executable.
func getExecutableDir() (string, error) {
	ex, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(ex), nil
}

// Phase 1.2: getPlatformConfigDir returns the platform-specific config directory.
func getPlatformConfigDir() (string, error) {
	if runtime.GOOS == "windows" {
		appData := os.Getenv("APPDATA")
		if appData == "" {
			return "", fmt.Errorf("APPDATA environment variable not set")
		}
		return filepath.Join(appData, "sfpg"), nil
	}
	// Unix-like systems (Linux, macOS)
	home := os.Getenv("HOME")
	if home == "" {
		return "", fmt.Errorf("HOME environment variable not set")
	}
	return filepath.Join(home, ".config", "sfpg"), nil
}

// Phase 1.3: fileExists checks if a path exists and is a readable file (not a directory).
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// FindConfigFiles returns a list of config.yaml paths in precedence order.
// Returns: [exeDir/config.yaml, platformDir/config.yaml]
// This is exported for use by Config.LoadFromYAML().
func FindConfigFiles() ([]string, error) {
	var configPaths []string

	// Check exe directory
	exeDir, err := getExecutableDir()
	if err == nil {
		exePath := filepath.Join(exeDir, "config.yaml")
		configPaths = append(configPaths, exePath)
	}

	// Check platform config directory
	platformDir, err := getPlatformConfigDir()
	if err == nil {
		platformPath := filepath.Join(platformDir, "config.yaml")
		configPaths = append(configPaths, platformPath)
	}

	return configPaths, nil
}

// validateOpt ensures Opt values fall within acceptable ranges.
func validateOpt(opt *Opt) error {
	// Skip port validation when using unlock-account (it's a database operation, not a server)
	if !opt.UnlockAccount.IsSet || opt.UnlockAccount.String == "" {
		if opt.Port.IsSet && (opt.Port.Int < 1 || opt.Port.Int > 65535) {
			return fmt.Errorf("port must be between 1 and 65535")
		}
	}
	if opt.DebugDelayMS.IsSet && opt.DebugDelayMS.Int < 0 {
		opt.DebugDelayMS.Int = 0
	}
	// Skip session secret validation when using unlock-account (database operation doesn't need sessions)
	if (!opt.UnlockAccount.IsSet || opt.UnlockAccount.String == "") && (!opt.SessionSecret.IsSet || opt.SessionSecret.String == "") {
		return fmt.Errorf("session-secret is required (set via SEPG_SESSION_SECRET environment variable or -session-secret flag)")
	}
	return nil
}

// applyEnvVars applies environment variable overrides to the provided Opt.
// Sets IsSet=true for any values that are explicitly set via environment variables.
func applyEnvVars(opt *Opt) {
	if v := strings.TrimSpace(os.Getenv("SFG_PORT")); v != "" {
		if p, err := strconv.Atoi(v); err == nil {
			opt.Port.Int = p
			opt.Port.IsSet = true
		} else {
			usageExit(fmt.Sprintf("invalid SFG_PORT: %v", err))
		}
	}
	if v := strings.TrimSpace(os.Getenv("SFG_DISCOVER")); v != "" {
		if b, err := parseBoolEnv(v); err == nil {
			opt.RunFileDiscovery.Bool = b
			opt.RunFileDiscovery.IsSet = true
		} else {
			usageExit(fmt.Sprintf("invalid SFG_DISCOVER: %v", err))
		}
	}
	if v := strings.TrimSpace(os.Getenv("SFG_DEBUG_DELAY_MS")); v != "" {
		if d, err := strconv.Atoi(v); err == nil {
			opt.DebugDelayMS.Int = d
			opt.DebugDelayMS.IsSet = true
		} else {
			usageExit(fmt.Sprintf("invalid SFG_DEBUG_DELAY_MS: %v", err))
		}
	}
	if v := strings.TrimSpace(os.Getenv("SFG_PROFILE")); v != "" {
		opt.Profile.String = v
		opt.Profile.IsSet = true
	}
	if v := strings.TrimSpace(os.Getenv("SFG_COMPRESSION")); v != "" {
		if b, err := parseBoolEnv(v); err == nil {
			opt.EnableCompression.Bool = b
			opt.EnableCompression.IsSet = true
		} else {
			usageExit(fmt.Sprintf("invalid SFG_COMPRESSION: %v", err))
		}
	}
	if v := strings.TrimSpace(os.Getenv("SFG_HTTP_CACHE")); v != "" {
		if b, err := parseBoolEnv(v); err == nil {
			opt.EnableHTTPCache.Bool = b
			opt.EnableHTTPCache.IsSet = true
		} else {
			usageExit(fmt.Sprintf("invalid SFG_HTTP_CACHE: %v", err))
		}
	}
	if v := strings.TrimSpace(os.Getenv("SFG_CACHE_PRELOAD")); v != "" {
		if b, err := parseBoolEnv(v); err == nil {
			opt.EnableCachePreload.Bool = b
			opt.EnableCachePreload.IsSet = true
		} else {
			usageExit(fmt.Sprintf("invalid SFG_CACHE_PRELOAD: %v", err))
		}
	}
	if v := strings.TrimSpace(os.Getenv("SEPG_SESSION_SECRET")); v != "" {
		opt.SessionSecret.String = v
		opt.SessionSecret.IsSet = true
	}
	if v := strings.TrimSpace(os.Getenv("SEPG_SESSION_SECURE")); v != "" {
		if b, err := parseBoolEnv(v); err == nil {
			opt.SessionSecure.Bool = b
			opt.SessionSecure.IsSet = true
		} else {
			usageExit(fmt.Sprintf("invalid SEPG_SESSION_SECURE: %v", err))
		}
	}
	if v := strings.TrimSpace(os.Getenv("SEPG_SESSION_HTTPONLY")); v != "" {
		if b, err := parseBoolEnv(v); err == nil {
			opt.SessionHttpOnly.Bool = b
			opt.SessionHttpOnly.IsSet = true
		} else {
			usageExit(fmt.Sprintf("invalid SEPG_SESSION_HTTPONLY: %v", err))
		}
	}
	if v := strings.TrimSpace(os.Getenv("SEPG_SESSION_MAX_AGE")); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			opt.SessionMaxAge.Int = i
			opt.SessionMaxAge.IsSet = true
		} else {
			usageExit(fmt.Sprintf("invalid SEPG_SESSION_MAX_AGE: %v", err))
		}
	}
	if v := strings.TrimSpace(os.Getenv("SEPG_SESSION_SAMESITE")); v != "" {
		opt.SessionSameSite.String = v
		opt.SessionSameSite.IsSet = true
	}
	if v := strings.TrimSpace(os.Getenv("SFG_UNLOCK_ACCOUNT")); v != "" {
		opt.UnlockAccount.String = v
		opt.UnlockAccount.IsSet = true
	}
	if v := strings.TrimSpace(os.Getenv("SFG_RESTORE_LAST_KNOWN_GOOD")); v != "" {
		if b, err := parseBoolEnv(v); err == nil {
			opt.RestoreLastKnownGood.Bool = b
			opt.RestoreLastKnownGood.IsSet = true
		} else {
			usageExit(fmt.Sprintf("invalid SFG_RESTORE_LAST_KNOWN_GOOD: %v", err))
		}
	}
}

// applyCLIFlags parses CLI flags and sets IsSet=true for any flags that are provided.
// CLI flags override environment variables (higher precedence).
func applyCLIFlags(opt *Opt) error {
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	// Use zero values as defaults for flag parsing (not opt values)
	port := fs.Int("port", 0, "TCP port for the HTTP server")
	discover := fs.Bool("discover", false, "Run discovery on startup")
	restoreLastKnownGood := fs.Bool("restore-last-known-good", false, "Restore last known good configuration from database on startup")
	debugDelay := fs.Int("debug-delay-ms", 0, "Artificial debug delay in milliseconds")
	profile := fs.String("profile", "", "Profiling mode: '', 'cpu', 'mem', 'block', etc.")
	compression := fs.Bool("compression", false, "Enable gzip/brotli compression")
	httpCache := fs.Bool("http-cache", false, "Enable SQLite HTTP response caching")
	cachePreload := fs.Bool("cache-preload", false, "Enable cache preloading when folders are opened")
	unlockAccount := fs.String("unlock-account", "", "Unlock a locked account by username")
	incrementETag := fs.Bool("increment-etag", false, "Increment application-wide ETag version on startup")
	cacheBatchLoad := fs.Bool("cache-batch-load", false, "Run cache batch load (warm HTTP cache) and exit")

	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	// Check if flags were provided by checking if they differ from zero values
	// or by using flag.Visit to see which flags were set
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "port":
			opt.Port.Int = *port
			opt.Port.IsSet = true
		case "discover":
			opt.RunFileDiscovery.Bool = *discover
			opt.RunFileDiscovery.IsSet = true
		case "restore-last-known-good":
			opt.RestoreLastKnownGood.Bool = *restoreLastKnownGood
			opt.RestoreLastKnownGood.IsSet = true
		case "debug-delay-ms":
			opt.DebugDelayMS.Int = *debugDelay
			opt.DebugDelayMS.IsSet = true
		case "profile":
			opt.Profile.String = *profile
			opt.Profile.IsSet = true
		case "compression":
			opt.EnableCompression.Bool = *compression
			opt.EnableCompression.IsSet = true
		case "http-cache":
			opt.EnableHTTPCache.Bool = *httpCache
			opt.EnableHTTPCache.IsSet = true
		case "cache-preload":
			opt.EnableCachePreload.Bool = *cachePreload
			opt.EnableCachePreload.IsSet = true
		case "unlock-account":
			opt.UnlockAccount.String = *unlockAccount
			opt.UnlockAccount.IsSet = true
		case "increment-etag":
			opt.IncrementETag.Bool = *incrementETag
			opt.IncrementETag.IsSet = true
		case "cache-batch-load":
			opt.CacheBatchLoad.Bool = *cacheBatchLoad
			opt.CacheBatchLoad.IsSet = true
		}
	})

	return nil
}
