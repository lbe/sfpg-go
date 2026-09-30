// Package getopt parses runtime configuration from environment variables and command-line flags.
// Precedence: CLI flags > Environment variables.
package getopt

import (
	"errors"
	"flag"
	"fmt"
	"io"
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

// Opt holds all runtime options parsed from env and CLI flags.
// All fields use nullable types to track whether they were explicitly set.
type Opt struct {
	Port                 OptInt    // TCP port the HTTP server binds to
	RunFileDiscovery     OptBool   // Whether to run discovery on startup
	DebugDelayMS         OptInt    // Optional artificial delay for debug/testing
	Profile              OptString // Profiling mode: "", "cpu", "mem", "block", etc.
	EnableHTTPCache      OptBool   // Enable SQLite HTTP response caching
	EnableCachePreload   OptBool   // Enable cache preloading when folders are opened
	SessionSecret        OptString // Secret key for session cookie signing (required)
	SessionSecure        OptBool   // Restrict session cookies to HTTPS
	SessionHttpOnly      OptBool   // Set HttpOnly flag on session cookies
	SessionMaxAge        OptInt    // Max age for session cookies in seconds
	SessionSameSite      OptString // SameSite policy for session cookies (Strict, Lax, None)
	LoginRateLimitPerIP  OptInt    // Max login POST requests per IP per 60-second window (0 = disabled)
	UnlockAccount        OptString // Username to unlock (empty string if not set)
	RestoreLastKnownGood OptBool   // Restore last known good configuration from database on startup
	IncrementETag        OptBool   // Increment application-wide ETag version on startup
	CacheBatchLoad       OptBool   // Run cache batch load and exit (CLI one-shot)
	LogLevel             OptString // Application log level: debug, info, warn, error
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
		if errors.Is(err, flag.ErrHelp) {
			osExit(0)
		}
		usageExit(err.Error())
	}

	// Validate that required fields are set
	if err := validateOpt(&opt); err != nil {
		usageExit(err.Error())
	}

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

// osExit is a hookable exit function for testing.
var osExit = os.Exit

// usageExit is a hookable exit function for testing.
var usageExit = func(msg string) {
	if msg != "" {
		fmt.Fprintf(os.Stderr, "Error: %s\n\n", msg)
	}
	// The flag package already printed usage for many flag.Parse errors.
	if !strings.HasPrefix(msg, "flag ") {
		printCLIUsage(os.Stderr)
	}
	osExit(1)
}

// osGOOS is a testable hook for runtime.GOOS.
var osGOOS = func() string { return runtime.GOOS }

// osExecutable is a testable hook for os.Executable.
var osExecutable = os.Executable

// Phase 1.1: getExecutableDir returns the directory of the running executable.
func getExecutableDir() (string, error) {
	ex, err := osExecutable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(ex), nil
}

// Phase 1.2: getPlatformConfigDir returns the platform-specific config directory.
func getPlatformConfigDir() (string, error) {
	if osGOOS() == "windows" {
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
	if !opt.UnlockAccount.IsSet || opt.UnlockAccount.String == "" {
		if !opt.SessionSecret.IsSet || opt.SessionSecret.String == "" {
			return fmt.Errorf("session-secret is required (set via SEPG_SESSION_SECRET environment variable or -session-secret flag)")
		}
		if len(opt.SessionSecret.String) < 32 {
			return fmt.Errorf("session-secret must be at least 32 bytes long (got %d bytes); generate a strong random secret via: head -c 48 /dev/urandom | base64", len(opt.SessionSecret.String))
		}
	}
	if opt.LogLevel.IsSet {
		level := strings.ToLower(strings.TrimSpace(opt.LogLevel.String))
		if err := validateLogLevel(level); err != nil {
			return err
		}
		opt.LogLevel.String = level
	}
	return nil
}

func validateLogLevel(level string) error {
	switch level {
	case "debug", "info", "warn", "error":
		return nil
	default:
		return fmt.Errorf("log level must be one of: debug, info, warn, error")
	}
}

// ApplyEnvVars applies environment variable overrides to the provided Opt.
// Sets IsSet=true for any values that are explicitly set via environment variables.
func ApplyEnvVars(opt *Opt) {
	applyEnvVars(opt)
}

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
	if v := strings.TrimSpace(os.Getenv("SEPG_LOGIN_RATE_LIMIT_PER_IP")); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			opt.LoginRateLimitPerIP.Int = i
			opt.LoginRateLimitPerIP.IsSet = true
		} else {
			usageExit(fmt.Sprintf("invalid SEPG_LOGIN_RATE_LIMIT_PER_IP: %v", err))
		}
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
	if v := strings.TrimSpace(os.Getenv("SFG_LOG_LEVEL")); v != "" {
		level := strings.ToLower(v)
		if err := validateLogLevel(level); err != nil {
			usageExit(fmt.Sprintf("invalid SFG_LOG_LEVEL: %v", err))
		}
		opt.LogLevel.String = level
		opt.LogLevel.IsSet = true
	}
}

type cliFlagBindings struct {
	port                 *int
	discover             *bool
	restoreLastKnownGood *bool
	debugDelay           *int
	profile              *string
	httpCache            *bool
	cachePreload         *bool
	unlockAccount        *string
	incrementETag        *bool
	cacheBatchLoad       *bool
	logLevel             *string
}

func registerCLIFlags(fs *flag.FlagSet) cliFlagBindings {
	return cliFlagBindings{
		port:                 fs.Int("port", 0, "TCP port for the HTTP server"),
		discover:             fs.Bool("discover", false, "Run discovery on startup"),
		restoreLastKnownGood: fs.Bool("restore-last-known-good", false, "Restore last known good configuration from database on startup"),
		debugDelay:           fs.Int("debug-delay-ms", 0, "Artificial debug delay in milliseconds"),
		profile:              fs.String("profile", "", "Profiling mode: '', 'cpu', 'mem', 'block', etc."),
		httpCache:            fs.Bool("http-cache", false, "Enable SQLite HTTP response caching"),
		cachePreload:         fs.Bool("cache-preload", false, "Enable cache preloading when folders are opened"),
		unlockAccount:        fs.String("unlock-account", "", "Unlock a locked account by username"),
		incrementETag:        fs.Bool("increment-etag", false, "Increment application-wide ETag version on startup"),
		cacheBatchLoad:       fs.Bool("cache-batch-load", false, "Run cache batch load (warm HTTP cache) and exit"),
		logLevel:             fs.String("log-level", "", "Application log level: debug, info, warn, error"),
	}
}

func newCLIFlagSet(output io.Writer) (*flag.FlagSet, cliFlagBindings) {
	if output == nil {
		output = os.Stderr
	}
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(output)
	b := registerCLIFlags(fs)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage of %s:\n", fs.Name())
		fs.PrintDefaults()
	}
	return fs, b
}

func printCLIUsage(w io.Writer) {
	fs, _ := newCLIFlagSet(w)
	fs.Usage()
}

func applyCLIFlagVisit(fs *flag.FlagSet, b cliFlagBindings, opt *Opt) {
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "port":
			opt.Port.Int = *b.port
			opt.Port.IsSet = true
		case "discover":
			opt.RunFileDiscovery.Bool = *b.discover
			opt.RunFileDiscovery.IsSet = true
		case "restore-last-known-good":
			opt.RestoreLastKnownGood.Bool = *b.restoreLastKnownGood
			opt.RestoreLastKnownGood.IsSet = true
		case "debug-delay-ms":
			opt.DebugDelayMS.Int = *b.debugDelay
			opt.DebugDelayMS.IsSet = true
		case "profile":
			opt.Profile.String = *b.profile
			opt.Profile.IsSet = true
		case "http-cache":
			opt.EnableHTTPCache.Bool = *b.httpCache
			opt.EnableHTTPCache.IsSet = true
		case "cache-preload":
			opt.EnableCachePreload.Bool = *b.cachePreload
			opt.EnableCachePreload.IsSet = true
		case "unlock-account":
			opt.UnlockAccount.String = *b.unlockAccount
			opt.UnlockAccount.IsSet = true
		case "increment-etag":
			opt.IncrementETag.Bool = *b.incrementETag
			opt.IncrementETag.IsSet = true
		case "cache-batch-load":
			opt.CacheBatchLoad.Bool = *b.cacheBatchLoad
			opt.CacheBatchLoad.IsSet = true
		case "log-level":
			opt.LogLevel.String = *b.logLevel
			opt.LogLevel.IsSet = true
		}
	})
}

// applyCLIFlags parses CLI flags and sets IsSet=true for any flags that are provided.
// CLI flags override environment variables (higher precedence).
func applyCLIFlags(opt *Opt) error {
	fs, b := newCLIFlagSet(os.Stderr)

	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	applyCLIFlagVisit(fs, b, opt)
	return nil
}
