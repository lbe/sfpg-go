package getopt

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func resetEnv() {
	os.Unsetenv("SFG_PORT")
	os.Unsetenv("SFG_DISCOVER")
	os.Unsetenv("SFG_DEBUG_DELAY_MS")
	os.Unsetenv("SFG_PROFILE")
	os.Unsetenv("SFG_COMPRESSION")
	os.Unsetenv("SFG_HTTP_CACHE")
	os.Unsetenv("SFG_CACHE_PRELOAD")
	os.Unsetenv("SEPG_SESSION_SECRET")
	os.Unsetenv("SEPG_SESSION_SECURE")
	os.Unsetenv("SEPG_SESSION_HTTPONLY")
	os.Unsetenv("SEPG_SESSION_MAX_AGE")
	os.Unsetenv("SEPG_SESSION_SAMESITE")
}

func resetFlags() {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	os.Args = []string{"cmd"}
}

func TestParse_NegativeDelayCoerced(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")
	os.Args = []string{"cmd", "-debug-delay-ms=-10"}
	opt := Parse()
	if opt.DebugDelayMS.Int != 0 {
		t.Fatalf("expected coerced debugDelayMS 0, got %d", opt.DebugDelayMS.Int)
	}
}

func TestParse_CompressionDefault(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")
	opt := Parse()
	// No defaults - should be zero value (false) and not set
	if opt.EnableCompression.Bool != false {
		t.Fatalf("expected zero value compression false, got %v", opt.EnableCompression.Bool)
	}
	if opt.EnableCompression.IsSet != false {
		t.Fatalf("expected EnableCompression.IsSet=false, got %v", opt.EnableCompression.IsSet)
	}
}

func TestParse_CompressionEnvVar(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")
	os.Setenv("SFG_COMPRESSION", "false")
	opt := Parse()
	if opt.EnableCompression.Bool != false {
		t.Fatalf("expected compression false from env, got %v", opt.EnableCompression.Bool)
	}
}

func TestParse_CompressionFlag(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")
	os.Args = []string{"cmd", "-compression=false"}
	opt := Parse()
	if opt.EnableCompression.Bool != false {
		t.Fatalf("expected compression false from flag, got %v", opt.EnableCompression.Bool)
	}
}

func TestParse_HTTPCacheDefault(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")
	opt := Parse()
	// No defaults - should be zero value (false) and not set
	if opt.EnableHTTPCache.Bool != false {
		t.Fatalf("expected zero value http-cache false, got %v", opt.EnableHTTPCache.Bool)
	}
	if opt.EnableHTTPCache.IsSet != false {
		t.Fatalf("expected EnableHTTPCache.IsSet=false, got %v", opt.EnableHTTPCache.IsSet)
	}
}

func TestParse_HTTPCacheEnvVar(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")
	os.Setenv("SFG_HTTP_CACHE", "false")
	opt := Parse()
	if opt.EnableHTTPCache.Bool != false {
		t.Fatalf("expected http-cache false from env, got %v", opt.EnableHTTPCache.Bool)
	}
}

func TestParse_HTTPCacheFlag(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")
	os.Args = []string{"cmd", "-http-cache=false"}
	opt := Parse()
	if opt.EnableHTTPCache.Bool != false {
		t.Fatalf("expected http-cache false from flag, got %v", opt.EnableHTTPCache.Bool)
	}
}

func TestParse_CachePreloadFlag(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")
	os.Args = []string{"cmd", "-cache-preload=true"}
	opt := Parse()
	if !opt.EnableCachePreload.IsSet {
		t.Fatalf("expected EnableCachePreload.IsSet=true when flag provided")
	}
	if !opt.EnableCachePreload.Bool {
		t.Fatalf("expected cache-preload true from flag, got %v", opt.EnableCachePreload.Bool)
	}
}

func TestParse_CachePreloadEnvVar(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")
	os.Setenv("SFG_CACHE_PRELOAD", "false")
	opt := Parse()
	if !opt.EnableCachePreload.IsSet {
		t.Fatalf("expected EnableCachePreload.IsSet=true when env var set")
	}
	if opt.EnableCachePreload.Bool {
		t.Fatalf("expected cache-preload false from env, got %v", opt.EnableCachePreload.Bool)
	}
}

func TestParse_UnlockAccountFlag(t *testing.T) {
	resetEnv()
	resetFlags()
	// UnlockAccount flag should work without SessionSecret (it's a database operation)
	os.Args = []string{"cmd", "-unlock-account=testuser"}
	opt := Parse()
	if opt.UnlockAccount.String != "testuser" {
		t.Fatalf("expected UnlockAccount 'testuser' from flag, got %q", opt.UnlockAccount.String)
	}
}

func TestParse_UnlockAccountFlag_Empty(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")
	os.Args = []string{"cmd"}
	opt := Parse()
	if opt.UnlockAccount.String != "" {
		t.Fatalf("expected UnlockAccount empty string by default, got %q", opt.UnlockAccount.String)
	}
}

// Phase 1.1: getExecutableDir tests
func TestGetExecutableDir(t *testing.T) {
	// Cannot use t.Parallel() - os.Executable() may access os.Args[0]

	dir, err := getExecutableDir()
	if err != nil {
		t.Fatalf("getExecutableDir failed: %v", err)
	}

	// Should return non-empty string
	if dir == "" {
		t.Error("expected non-empty directory, got empty string")
	}

	// Should be a valid directory
	info, err := os.Stat(dir)
	if err != nil {
		t.Errorf("directory does not exist or cannot be accessed: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("path is not a directory: %s", dir)
	}
}

// Phase 1.2: getPlatformConfigDir tests
func TestGetPlatformConfigDir_Unix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test for Unix systems only")
	}

	t.Setenv("HOME", "/home/testuser")

	dir, err := getPlatformConfigDir()
	if err != nil {
		t.Fatalf("getPlatformConfigDir failed: %v", err)
	}

	expected := filepath.Join("/home/testuser", ".config", "sfpg")
	if dir != expected {
		t.Errorf("expected %q, got %q", expected, dir)
	}
}

func TestGetPlatformConfigDir_NoHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("APPDATA", "")

	dir, err := getPlatformConfigDir()
	if err == nil {
		t.Errorf("expected error when HOME/APPDATA not set, got nil")
	}
	if dir != "" {
		t.Errorf("expected empty string on error, got %q", dir)
	}
}

// Phase 1.3: fileExists tests
func TestFileExists_FilePresent(t *testing.T) {
	t.Parallel()

	tmpFile, err := os.CreateTemp("", "test_*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	if !fileExists(tmpFile.Name()) {
		t.Error("fileExists returned false for existing file")
	}
}

func TestFileExists_FileAbsent(t *testing.T) {
	t.Parallel()

	if fileExists("/nonexistent/path/to/file.yaml") {
		t.Error("fileExists returned true for non-existent file")
	}
}

func TestFileExists_IsDirectory(t *testing.T) {
	t.Parallel()

	tmpDir, err := os.MkdirTemp("", "test_dir")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	if fileExists(tmpDir) {
		t.Error("fileExists returned true for directory")
	}
}

// Phase 1.4: FindConfigFiles tests
func TestFindConfigFiles_None(t *testing.T) {
	t.Setenv("HOME", "/nonexistent/home/path")
	t.Setenv("APPDATA", "/nonexistent/appdata")

	files, err := FindConfigFiles()
	if err != nil {
		t.Fatalf("FindConfigFiles failed: %v", err)
	}

	if len(files) != 2 {
		t.Errorf("expected 2 config file paths, got %d", len(files))
	}
}

func TestFindConfigFiles_ReturnsSlice(t *testing.T) {
	t.Parallel()

	// This test verifies the logic returns a slice of paths
	files, err := FindConfigFiles()
	if err != nil {
		t.Fatalf("FindConfigFiles failed: %v", err)
	}
	if files == nil {
		t.Error("FindConfigFiles returned nil")
	}
}

// Phase 3.1: validateOpt tests
func TestValidateOpt_ValidPort(t *testing.T) {
	t.Parallel()

	ports := []int{1, 80, 8080, 65535}

	for _, p := range ports {
		t.Run(fmt.Sprintf("port_%d", p), func(t *testing.T) {
			t.Parallel()
			opt := Opt{
				Port:          OptInt{Int: p, IsSet: true},
				DebugDelayMS:  OptInt{Int: 0, IsSet: true},
				SessionSecret: OptString{String: "test-secret", IsSet: true},
			}
			if err := validateOpt(&opt); err != nil {
				t.Fatalf("expected valid port %d, got error: %v", p, err)
			}
		})
	}
}

func TestValidateOpt_InvalidPort_TooLow(t *testing.T) {
	t.Parallel()

	opt := Opt{
		Port:          OptInt{Int: 0, IsSet: true},
		SessionSecret: OptString{String: "test-secret", IsSet: true},
	}
	err := validateOpt(&opt)

	if err == nil {
		t.Fatal("expected error for port 0")
	}
	if !strings.Contains(err.Error(), "port") {
		t.Errorf("expected error to mention 'port', got: %v", err)
	}
}

func TestParse_IncrementETag(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")

	tests := []struct {
		name     string
		args     []string
		wantSet  bool
		wantBool bool
	}{
		{
			name:     "flag not provided",
			args:     []string{"cmd"},
			wantSet:  false,
			wantBool: false,
		},
		{
			name:     "flag provided",
			args:     []string{"cmd", "-increment-etag"},
			wantSet:  true,
			wantBool: true,
		},
		{
			name:     "flag with other flags",
			args:     []string{"cmd", "-port", "9090", "-increment-etag"},
			wantSet:  true,
			wantBool: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetFlags()
			os.Args = tt.args
			opt := Parse()

			if opt.IncrementETag.IsSet != tt.wantSet {
				t.Errorf("IncrementETag.IsSet = %v, want %v", opt.IncrementETag.IsSet, tt.wantSet)
			}
			if opt.IncrementETag.Bool != tt.wantBool {
				t.Errorf("IncrementETag.Bool = %v, want %v", opt.IncrementETag.Bool, tt.wantBool)
			}
		})
	}
}

func TestValidateOpt_InvalidPort_TooHigh(t *testing.T) {
	t.Parallel()

	opt := Opt{
		Port:          OptInt{Int: 65536, IsSet: true},
		SessionSecret: OptString{String: "test-secret", IsSet: true},
	}
	err := validateOpt(&opt)

	if err == nil {
		t.Fatal("expected error for port 65536")
	}
}

func TestValidateOpt_NegativeDebugDelay(t *testing.T) {
	t.Parallel()

	opt := Opt{
		Port:          OptInt{Int: 8080, IsSet: true},
		DebugDelayMS:  OptInt{Int: -100, IsSet: true},
		SessionSecret: OptString{String: "test-secret", IsSet: true},
	}
	err := validateOpt(&opt)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if opt.DebugDelayMS.Int != 0 {
		t.Errorf("expected delay clamped to 0, got %d", opt.DebugDelayMS.Int)
	}
}

// Phase 3.2: applyEnvVars tests
func TestApplyEnvVars_AllSet(t *testing.T) {
	t.Setenv("SFG_PORT", "9000")
	t.Setenv("SFG_DISCOVER", "false")
	t.Setenv("SFG_DEBUG_DELAY_MS", "50")
	t.Setenv("SFG_PROFILE", "cpu")
	t.Setenv("SFG_COMPRESSION", "false")
	t.Setenv("SFG_HTTP_CACHE", "true")

	opt := defaultOpt()
	applyEnvVars(&opt)

	if opt.Port.Int != 9000 {
		t.Errorf("expected port 9000, got %d", opt.Port.Int)
	}
	if opt.RunFileDiscovery.Bool != false {
		t.Error("expected discover false")
	}
	if opt.DebugDelayMS.Int != 50 {
		t.Errorf("expected debug delay 50, got %d", opt.DebugDelayMS.Int)
	}
	if opt.Profile.String != "cpu" {
		t.Errorf("expected profile cpu, got %q", opt.Profile.String)
	}
	if opt.EnableCompression.Bool != false {
		t.Errorf("expected compression false, got %v", opt.EnableCompression.Bool)
	}
	if opt.EnableHTTPCache.Bool != true {
		t.Errorf("expected http-cache true, got %v", opt.EnableHTTPCache.Bool)
	}
}

func TestApplyEnvVars_Partial(t *testing.T) {
	t.Setenv("SFG_PORT", "9100")

	opt := defaultOpt()
	applyEnvVars(&opt)

	if opt.Port.Int != 9100 {
		t.Errorf("expected port 9100, got %d", opt.Port.Int)
	}
	if opt.RunFileDiscovery.Bool != false {
		t.Error("expected discover false (default zero value)")
	}
}

// Phase 3.3: applyCLIFlags tests
func TestApplyCLIFlags_AllSet(t *testing.T) {
	// Cannot use t.Parallel() - modifies global os.Args

	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"prog", "-port", "9200", "-discover=false", "-debug-delay-ms", "75", "-profile", "cpu", "-compression=false", "-http-cache=false"}

	opt := defaultOpt()
	if err := applyCLIFlags(&opt); err != nil {
		t.Fatalf("applyCLIFlags returned error: %v", err)
	}

	if opt.Port.Int != 9200 {
		t.Errorf("expected port 9200, got %d", opt.Port.Int)
	}
	if opt.RunFileDiscovery.Bool != false {
		t.Error("expected discover false")
	}
	if opt.DebugDelayMS.Int != 75 {
		t.Errorf("expected debug delay 75, got %d", opt.DebugDelayMS.Int)
	}
	if opt.Profile.String != "cpu" {
		t.Errorf("expected profile cpu, got %q", opt.Profile.String)
	}
	if opt.EnableCompression.Bool != false {
		t.Errorf("expected compression false, got %v", opt.EnableCompression.Bool)
	}
	if opt.EnableHTTPCache.Bool != false {
		t.Errorf("expected http-cache false, got %v", opt.EnableHTTPCache.Bool)
	}
}

func TestApplyCLIFlags_DefaultsWhenNoArgs(t *testing.T) {
	// Cannot use t.Parallel() - modifies global os.Args

	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"prog"}

	opt := defaultOpt()
	if err := applyCLIFlags(&opt); err != nil {
		t.Fatalf("applyCLIFlags returned error: %v", err)
	}

	if opt.Port.Int != defaultOpt().Port.Int {
		t.Errorf("expected default port, got %d", opt.Port.Int)
	}
}

// Phase 4: Parse integration tests
func TestParse_Precedence_CLIOverridesEnv(t *testing.T) {
	resetEnv()
	resetFlags()

	// Env variable
	t.Setenv("SEPG_SESSION_SECRET", "test-secret-precedence")
	t.Setenv("SFG_PORT", "8084")

	// CLI override
	os.Args = []string{"prog", "-port", "8085"}

	opt := Parse()

	if opt.Port.Int != 8085 {
		t.Fatalf("expected port 8085 from CLI > env, got %d", opt.Port.Int)
	}
}

// SessionSecret tests
func TestParse_SessionSecret_FromEnv(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "my-secret-key")
	opt := Parse()
	if opt.SessionSecret.String != "my-secret-key" {
		t.Fatalf("expected session secret 'my-secret-key', got %q", opt.SessionSecret.String)
	}
}

func TestParse_SessionSecret_MissingTriggersError(t *testing.T) {
	resetEnv()
	resetFlags()

	oldExit := getUsageExit()
	defer func() { setUsageExit(oldExit) }()

	var exitMsg string
	setUsageExit(func(msg string) {
		exitMsg = msg
		panic(msg)
	})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected usageExit panic when session secret is missing")
		}
		if !strings.Contains(exitMsg, "session-secret is required") {
			t.Fatalf("expected error message about session-secret being required, got: %s", exitMsg)
		}
	}()

	Parse()
}

// TestParseBoolEnv_VariousInputs verifies parseBoolEnv correctly parses boolean values
func TestParseBoolEnv_VariousInputs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
		wantErr  bool
	}{
		{"true", "true", true, false},
		{"false", "false", false, false},
		{"1", "1", true, false},
		{"0", "0", false, false},
		{"yes", "yes", true, false},
		{"no", "no", false, false},
		{"invalid", "invalid", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseBoolEnv(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseBoolEnv(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if result != tt.expected {
				t.Errorf("parseBoolEnv(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestGetExecutableDir_ReturnsValidPath verifies getExecutableDir returns valid path
func TestGetExecutableDir_ReturnsValidPath(t *testing.T) {
	dir, err := getExecutableDir()
	if err != nil {
		t.Fatalf("getExecutableDir() should not fail: %v", err)
	}
	if dir == "" {
		t.Fatal("getExecutableDir() returned empty string")
	}
}

// TestParse_SessionSecureEnvVar verifies SEPG_SESSION_SECURE environment variable is parsed
func TestParse_SessionSecureEnvVar(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")
	os.Setenv("SEPG_SESSION_SECURE", "false")

	opt := Parse()

	if !opt.SessionSecure.IsSet {
		t.Error("expected SessionSecure.IsSet=true when SEPG_SESSION_SECURE is set")
	}
	if opt.SessionSecure.Bool != false {
		t.Errorf("expected SessionSecure.Bool=false, got %v", opt.SessionSecure.Bool)
	}
}

// TestParse_SessionHttpOnlyEnvVar verifies SEPG_SESSION_HTTPONLY environment variable is parsed
func TestParse_SessionHttpOnlyEnvVar(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")
	os.Setenv("SEPG_SESSION_HTTPONLY", "false")

	opt := Parse()

	if !opt.SessionHttpOnly.IsSet {
		t.Error("expected SessionHttpOnly.IsSet=true when SEPG_SESSION_HTTPONLY is set")
	}
	if opt.SessionHttpOnly.Bool != false {
		t.Errorf("expected SessionHttpOnly.Bool=false, got %v", opt.SessionHttpOnly.Bool)
	}
}

// TestParse_SessionMaxAgeEnvVar verifies SEPG_SESSION_MAX_AGE environment variable is parsed
func TestParse_SessionMaxAgeEnvVar(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")
	os.Setenv("SEPG_SESSION_MAX_AGE", "3600")

	opt := Parse()

	if !opt.SessionMaxAge.IsSet {
		t.Error("expected SessionMaxAge.IsSet=true when SEPG_SESSION_MAX_AGE is set")
	}
	if opt.SessionMaxAge.Int != 3600 {
		t.Errorf("expected SessionMaxAge.Int=3600, got %d", opt.SessionMaxAge.Int)
	}
}

// TestParse_SessionSameSiteEnvVar verifies SEPG_SESSION_SAMESITE environment variable is parsed
func TestParse_SessionSameSiteEnvVar(t *testing.T) {
	resetEnv()
	resetFlags()
	os.Setenv("SEPG_SESSION_SECRET", "test-secret")
	os.Setenv("SEPG_SESSION_SAMESITE", "Strict")

	opt := Parse()

	if !opt.SessionSameSite.IsSet {
		t.Error("expected SessionSameSite.IsSet=true when SEPG_SESSION_SAMESITE is set")
	}
	if opt.SessionSameSite.String != "Strict" {
		t.Errorf("expected SessionSameSite.String=Strict, got %s", opt.SessionSameSite.String)
	}
}
