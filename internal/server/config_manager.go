package server

import (
	"log/slog"
	"sync"

	"github.com/lbe/sfpg-go/internal/getopt"
	"github.com/lbe/sfpg-go/internal/server/config"
)

// ConfigManager owns application configuration state and ConfigService.
type ConfigManager struct {
	configMu      sync.RWMutex
	config        *config.Config
	configService config.ConfigService
}

func NewConfigManager() *ConfigManager {
	return &ConfigManager{}
}

func (m *ConfigManager) SetConfigService(svc config.ConfigService) { m.configService = svc }

func (m *ConfigManager) GetConfig() *config.Config {
	m.configMu.RLock()
	defer m.configMu.RUnlock()
	return m.config
}

func (m *ConfigManager) SetConfig(cfg *config.Config) {
	m.configMu.Lock()
	m.config = cfg
	m.configMu.Unlock()
}

func (m *ConfigManager) GetETagVersion() string {
	m.configMu.RLock()
	v := ""
	if m.config != nil {
		v = m.config.ETagVersion
	}
	m.configMu.RUnlock()
	if v == "" {
		return config.DefaultConfig().ETagVersion
	}
	return v
}

func (m *ConfigManager) UpdateConfigWithPrecedence(cfg *config.Config, changedFields []string, opt getopt.Opt) {
	m.configMu.Lock()
	m.config = cfg
	cfg.LoadFromOptExcluding(opt, changedFields)
	m.configMu.Unlock()
}

func (m *ConfigManager) logLoadedConfigDiagnostics(cfg *config.Config) {
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
