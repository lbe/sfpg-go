package config

import (
	"log/slog"
	"sync"

	"github.com/lbe/sfpg-go/internal/getopt"
)

// ConfigManager owns application configuration state and ConfigService.
type ConfigManager struct {
	ConfigMu      sync.RWMutex
	Config        *Config
	ConfigService ConfigService
}

// NewConfigManager constructs an empty configuration manager.
func NewConfigManager() *ConfigManager {
	return &ConfigManager{}
}

// SetConfigService wires the configuration service implementation.
func (m *ConfigManager) SetConfigService(svc ConfigService) { m.ConfigService = svc }

// GetConfig returns the current in-memory configuration snapshot.
func (m *ConfigManager) GetConfig() *Config {
	m.ConfigMu.RLock()
	defer m.ConfigMu.RUnlock()
	return m.Config
}

// SetConfig replaces the in-memory configuration snapshot.
func (m *ConfigManager) SetConfig(cfg *Config) {
	m.ConfigMu.Lock()
	m.Config = cfg
	m.ConfigMu.Unlock()
}

// GetETagVersion returns the current ETag cache-busting version.
func (m *ConfigManager) GetETagVersion() string {
	m.ConfigMu.RLock()
	v := ""
	if m.Config != nil {
		v = m.Config.ETagVersion
	}
	m.ConfigMu.RUnlock()
	if v == "" {
		return DefaultConfig().ETagVersion
	}
	return v
}

// UpdateConfigWithPrecedence stores cfg and reapplies CLI/env overrides except on changedFields.
func (m *ConfigManager) UpdateConfigWithPrecedence(cfg *Config, changedFields []string, opt getopt.Opt) {
	m.ConfigMu.Lock()
	m.Config = cfg
	cfg.LoadFromOptExcluding(opt, changedFields)
	m.ConfigMu.Unlock()
}

// LogLoadedConfigDiagnostics emits startup diagnostics for loaded configuration.
func (m *ConfigManager) LogLoadedConfigDiagnostics(cfg *Config) {
	m.logLoadedConfigDiagnostics(cfg)
}

func (m *ConfigManager) logLoadedConfigDiagnostics(cfg *Config) {
	if cfg == nil {
		return
	}
	if err := cfg.Validate(); err != nil {
		slog.Warn("loaded configuration failed strict validation", "err", err)
	}
	for _, w := range cfg.ValidateGuardrails() {
		slog.Warn("configuration guardrail warning",
			"check", w.Check, "configured", w.Configured,
			"effective", w.Effective, "hint", w.Hint)
	}
}
