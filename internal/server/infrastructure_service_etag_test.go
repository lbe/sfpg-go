package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lbe/sfpg-go/internal/server/config"
)

// mockConfigServiceForInfraETag is a minimal config.ConfigService double for
// InfrastructureService ETag tests.
type mockConfigServiceForInfraETag struct {
	loadReturn  *config.Config
	loadErr     error
	validateErr error
	saveErr     error
	saveCalled  bool
	savedConfig *config.Config
}

func (m *mockConfigServiceForInfraETag) Load(ctx context.Context) (*config.Config, error) {
	if m.loadErr != nil {
		return nil, m.loadErr
	}
	return m.loadReturn, nil
}

func (m *mockConfigServiceForInfraETag) Save(ctx context.Context, cfg *config.Config) error {
	m.saveCalled = true
	m.savedConfig = cfg
	return m.saveErr
}

func (m *mockConfigServiceForInfraETag) Validate(cfg *config.Config) error {
	return m.validateErr
}

func (m *mockConfigServiceForInfraETag) Export(ctx context.Context) (string, error) {
	return "", nil
}

func (m *mockConfigServiceForInfraETag) Import(yamlContent string, ctx context.Context) error {
	return nil
}

func (m *mockConfigServiceForInfraETag) RestoreLastKnownGood(ctx context.Context) (*config.Config, error) {
	return nil, nil
}

func (m *mockConfigServiceForInfraETag) EnsureDefaults(ctx context.Context, rootDir string) error {
	return nil
}

func (m *mockConfigServiceForInfraETag) GetConfigValue(ctx context.Context, key string) (string, error) {
	return "", nil
}

func (m *mockConfigServiceForInfraETag) IncrementETag(ctx context.Context) (string, error) {
	return "", nil
}

func TestInfrastructureService_IncrementETag_ValidationFailure(t *testing.T) {
	ctx := context.Background()
	infra := NewInfrastructureService()

	cfg := config.DefaultConfig()
	cfg.ETagVersion = "20260701-01"

	mockSvc := &mockConfigServiceForInfraETag{
		loadReturn:  cfg,
		validateErr: errors.New("listener port out of range"),
	}

	_, err := infra.IncrementETag(ctx, mockSvc)
	if err == nil {
		t.Fatal("IncrementETag expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid config after ETag increment") {
		t.Fatalf("IncrementETag error = %v, want wrapped validation error", err)
	}
	if mockSvc.saveCalled {
		t.Error("Save should not be called when validation fails")
	}
}
