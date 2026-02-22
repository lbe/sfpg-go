package server

import (
	"context"
	"slices"
	"testing"

	"go.local/sfpg/internal/getopt"
	"go.local/sfpg/internal/server/config"
)

// recordingConfigService implements config.ConfigService and records calls to
// EnsureDefaults and GetConfigValue for delegation tests (Task 9.3).
type recordingConfigService struct {
	ensureDefaultsCalled bool
	ensureDefaultsRoot   string
	getConfigValueKeys   []string
	getConfigValueVal    string
	getConfigValueErr    error
}

func (r *recordingConfigService) Load(ctx context.Context) (*config.Config, error) {
	return config.DefaultConfig(), nil
}

func (r *recordingConfigService) Save(ctx context.Context, cfg *config.Config) error {
	return nil
}

func (r *recordingConfigService) Validate(cfg *config.Config) error {
	return nil
}

func (r *recordingConfigService) Export() (string, error) {
	return "", nil
}

func (r *recordingConfigService) Import(yamlContent string, ctx context.Context) error {
	return nil
}

func (r *recordingConfigService) RestoreLastKnownGood(ctx context.Context) (*config.Config, error) {
	return config.DefaultConfig(), nil
}

func (r *recordingConfigService) EnsureDefaults(ctx context.Context, rootDir string) error {
	r.ensureDefaultsCalled = true
	r.ensureDefaultsRoot = rootDir
	return nil
}

func (r *recordingConfigService) GetConfigValue(ctx context.Context, key string) (string, error) {
	r.getConfigValueKeys = append(r.getConfigValueKeys, key)
	if r.getConfigValueErr != nil {
		return "", r.getConfigValueErr
	}
	return r.getConfigValueVal, nil
}

func (r *recordingConfigService) IncrementETag(ctx context.Context) (string, error) {
	return "20260129-01", nil
}

// Test_setConfigDefaults_delegates_to_ConfigService_EnsureDefaults verifies that
// setConfigDefaults delegates to ConfigService.EnsureDefaults instead of using
// the database directly. Red phase: fails until App delegates.
func Test_setConfigDefaults_delegates_to_ConfigService_EnsureDefaults(t *testing.T) {
	tempDir := t.TempDir()
	opt := getopt.Opt{SessionSecret: getopt.OptString{String: "test-secret", IsSet: true}}
	app := New(opt, "x.y.z")
	defer app.Shutdown()
	app.setRootDir(&tempDir)
	app.setDB()
	rec := &recordingConfigService{}
	app.configService = rec
	rootDir := app.rootDir

	app.setConfigDefaults()

	if !rec.ensureDefaultsCalled {
		t.Error("setConfigDefaults should call ConfigService.EnsureDefaults")
	}
	if rec.ensureDefaultsRoot != rootDir {
		t.Errorf("EnsureDefaults should be called with rootDir %q, got %q", rootDir, rec.ensureDefaultsRoot)
	}
}

// Test_getAdminUsername_delegates_to_ConfigService_GetConfigValue verifies that
// getAdminUsername delegates to ConfigService.GetConfigValue("user") instead of
// using the database directly. Red phase: fails until App delegates.
func Test_getAdminUsername_delegates_to_ConfigService_GetConfigValue(t *testing.T) {
	app := CreateApp(t, false)
	defer app.Shutdown()
	rec := &recordingConfigService{getConfigValueVal: "admin"}
	app.configService = rec

	_, _ = app.getAdminUsername()

	if len(rec.getConfigValueKeys) == 0 {
		t.Fatal("getAdminUsername should call ConfigService.GetConfigValue")
	}
	found := slices.Contains(rec.getConfigValueKeys, "user")
	if !found {
		t.Errorf("GetConfigValue should be called with key %q, got %v", "user", rec.getConfigValueKeys)
	}
}
