package handlers

import (
	"context"
	"fmt"
	"html"
	"html/template"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.local/sfpg/internal/dbconnpool"
	"go.local/sfpg/internal/server/auth"
	"go.local/sfpg/internal/server/config"
	"go.local/sfpg/internal/server/session"
	"go.local/sfpg/internal/server/ui"
)

// ConfigTemplates holds the parsed config UI templates.
type ConfigTemplates struct {
	SaveRestartAlert      *template.Template
	SaveSuccessAlert      *template.Template
	ExportModal           *template.Template
	ImportModal           *template.Template
	ImportSuccessAlert    *template.Template
	RestoreModal          *template.Template
	RestoreSuccessAlert   *template.Template
	RestartInitiatedAlert *template.Template
}

// ConfigHandlers holds dependencies for configuration-related HTTP handlers.
// It has ~12 dependencies compared to ~35 in the main Handlers struct.
type ConfigHandlers struct {
	ConfigService   config.ConfigService
	AuthService     auth.AuthService
	CredentialStore auth.CredentialStore
	SessionManager  session.SessionManager
	DBRoPool        dbconnpool.ConnectionPool
	DBRwPool        dbconnpool.ConnectionPool
	Templates       ConfigTemplates
	Ctx             context.Context

	// Callbacks
	// UpdateConfig receives the new config and a list of fields that were changed by the user.
	// The callback should apply CLI/env overrides only to fields NOT in the changed list.
	UpdateConfig        func(cfg *config.Config, changedFields []string)
	ApplyConfig         func()
	IncrementETag       func() (string, error)
	InvalidateHTTPCache func()
	SetPreloadEnabled   func(bool)
	SetRestartRequired  func(bool)
	GetRestartCh        func() chan struct{}

	// Helpers
	AddCommonTemplateData func(http.ResponseWriter, *http.Request, map[string]any) map[string]any
	ServerError           func(http.ResponseWriter, *http.Request, error)
}

// NewConfigHandlers creates a new ConfigHandlers with the given dependencies.
func NewConfigHandlers(
	configService config.ConfigService,
	authService auth.AuthService,
	credentialStore auth.CredentialStore,
	sessionManager session.SessionManager,
	dbRoPool dbconnpool.ConnectionPool,
	dbRwPool dbconnpool.ConnectionPool,
	templates ConfigTemplates,
	ctx context.Context,
) *ConfigHandlers {
	return &ConfigHandlers{
		ConfigService:   configService,
		AuthService:     authService,
		CredentialStore: credentialStore,
		SessionManager:  sessionManager,
		DBRoPool:        dbRoPool,
		DBRwPool:        dbRwPool,
		Templates:       templates,
		Ctx:             ctx,
	}
}

// Validate ensures all required dependencies and callbacks are set.
func (h *ConfigHandlers) Validate() error {
	var missing []string

	// Basic dependencies (set in constructor)
	if h.ConfigService == nil {
		missing = append(missing, "ConfigService")
	}
	if h.AuthService == nil {
		missing = append(missing, "AuthService")
	}
	if h.CredentialStore == nil {
		missing = append(missing, "CredentialStore")
	}
	if h.SessionManager == nil {
		missing = append(missing, "SessionManager")
	}
	if h.DBRoPool == nil {
		missing = append(missing, "DBRoPool")
	}
	if h.DBRwPool == nil {
		missing = append(missing, "DBRwPool")
	}
	if h.Ctx == nil {
		missing = append(missing, "Ctx")
	}

	// Callbacks (set via field assignment)
	if h.UpdateConfig == nil {
		missing = append(missing, "UpdateConfig")
	}
	if h.ApplyConfig == nil {
		missing = append(missing, "ApplyConfig")
	}
	if h.IncrementETag == nil {
		missing = append(missing, "IncrementETag")
	}
	if h.InvalidateHTTPCache == nil {
		missing = append(missing, "InvalidateHTTPCache")
	}
	if h.SetPreloadEnabled == nil {
		missing = append(missing, "SetPreloadEnabled")
	}
	if h.SetRestartRequired == nil {
		missing = append(missing, "SetRestartRequired")
	}
	if h.GetRestartCh == nil {
		missing = append(missing, "GetRestartCh")
	}

	// Helpers
	if h.AddCommonTemplateData == nil {
		missing = append(missing, "AddCommonTemplateData")
	}
	if h.ServerError == nil {
		missing = append(missing, "ServerError")
	}

	if len(missing) > 0 {
		return fmt.Errorf("ConfigHandlers missing required fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

// disableConfigCaching sets HTTP headers to disable all caching for configuration routes.
func (h *ConfigHandlers) disableConfigCaching(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
}

func (h *ConfigHandlers) ensureCsrf(w http.ResponseWriter, r *http.Request) string {
	return h.SessionManager.EnsureCSRFToken(w, r)
}

func (h *ConfigHandlers) validateCsrf(r *http.Request) bool {
	return h.SessionManager.ValidateCSRFToken(r)
}

func (h *ConfigHandlers) executeConfigTemplate(w http.ResponseWriter, tmpl *template.Template, templateName string, data any) {
	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.Execute(w, data); err != nil {
		slog.Error("failed to execute config template",
			"template", templateName,
			"error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// ConfigGet handles GET /config requests and renders the comprehensive configuration modal with all settings.
// It retrieves the current configuration, loads help text and example values from the database,
// and renders the config-modal.html.tmpl template with the collected data.
// Authentication is required via the authMiddleware.
func (h *ConfigHandlers) ConfigGet(w http.ResponseWriter, r *http.Request) {
	h.disableConfigCaching(w)

	// Check authentication via SessionManager
	if !h.SessionManager.IsAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Load current config via ConfigService
	cfg, err := h.ConfigService.Load(h.Ctx)
	if err != nil {
		slog.Warn("failed to load config for display", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	username, err := h.ConfigService.GetConfigValue(h.Ctx, "user")
	if err != nil {
		slog.Warn("failed to get admin username", "err", err)
		username = "admin" // Default
	}

	// Prepare template data
	data := map[string]any{
		"Username":    username,
		"Config":      cfg,
		"ETagVersion": cfg.ETagVersion,
	}

	// Load help text and example values from database
	cpcRo, err := h.DBRoPool.Get()
	if err != nil {
		slog.Warn("failed to get DB connection for help text", "err", err)
	} else {
		defer h.DBRoPool.Put(cpcRo)

		configRows, cfgErr := cpcRo.Queries.GetConfigs(h.Ctx)
		if cfgErr != nil {
			slog.Warn("failed to load config metadata", "err", cfgErr)
		} else {
			helpTextMap := make(map[string]string)
			exampleValueMap := make(map[string]string)

			for _, cfgRow := range configRows {
				if cfgRow.HelpText.Valid {
					helpTextMap[cfgRow.Key] = cfgRow.HelpText.String
				}
				if cfgRow.ExampleValue.Valid {
					exampleValueMap[cfgRow.Key] = cfgRow.ExampleValue.String
				}
			}

			data["HelpText"] = helpTextMap
			data["ExampleValue"] = exampleValueMap
		}
	}

	// Check for category query parameter
	category := r.URL.Query().Get("category")
	if category != "" {
		data["Category"] = category
	}

	data = h.AddCommonTemplateData(w, r, data)

	// Render modal template
	if err := ui.RenderTemplate(w, "config-modal.html.tmpl", data); err != nil {
		slog.Error("failed to render config modal", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// ConfigPost handles POST /config requests and processes configuration setting updates.
// It validates the CSRF token, parses the form data, and applies configuration changes.
// If changes affect runtime properties (listener address, port, log settings), it marks
// the restart as required. Authentication is required via the authMiddleware.
func (h *ConfigHandlers) ConfigPost(w http.ResponseWriter, r *http.Request) {
	h.disableConfigCaching(w)

	if !h.SessionManager.IsAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := r.ParseForm(); err != nil {
		slog.Warn("failed to parse form in configPost", "err", err)
		w.Header().Set("HX-Retarget", "#config-error-message")
		w.Header().Set("HX-Swap", "outerHTML")
		w.WriteHeader(http.StatusBadRequest)
		if err := ui.RenderTemplate(w, "config-generic-error.html.tmpl", map[string]any{
			"Message": "Invalid form data",
		}); err != nil {
			slog.Error("failed to render generic error template", "err", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	if !h.validateCsrf(r) {
		slog.Warn("CSRF validation failed for config update", "remote_addr", r.RemoteAddr)
		http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
		return
	}

	// Load current config
	oldConfig, err := h.ConfigService.Load(h.Ctx)
	if err != nil {
		slog.Error("failed to load current config", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	currentUsername, err := h.ConfigService.GetConfigValue(h.Ctx, "user")
	if err != nil {
		slog.Warn("failed to get current username", "err", err)
	}

	// Process admin credential updates first
	result, err := h.AuthService.UpdateCredentials(h.Ctx, auth.CredentialUpdateOptions{
		CurrentUsername: currentUsername,
		NewUsername:     r.FormValue("admin_username"),
		CurrentPassword: r.FormValue("admin_current_password"),
		NewPassword:     r.FormValue("admin_new_password"),
		ConfirmPassword: r.FormValue("admin_confirm_password"),
	}, h.CredentialStore)

	if err != nil {
		slog.Error("failed to update admin credentials", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	validationErrors := make(map[string]string)
	if result != nil && len(result.ValidationErrors) > 0 {
		maps.Copy(validationErrors, result.ValidationErrors)
	}

	// Create a copy to modify
	newConfig := *oldConfig
	restartRequired := false
	restartRequiredKeys := []string{}

	// Config key setters
	configKeys := map[string]func(string) error{
		"listener_address": func(v string) error {
			if oldConfig.ListenerAddress != v {
				newConfig.ListenerAddress = v
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "listener_address")
			}
			return nil
		},
		"listener_port": func(v string) error {
			port, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("invalid port: %w", err)
			}
			if oldConfig.ListenerPort != port {
				newConfig.ListenerPort = port
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "listener_port")
			}
			return nil
		},
		"log_directory": func(v string) error {
			if oldConfig.LogDirectory != v {
				newConfig.LogDirectory = v
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "log_directory")
			}
			return nil
		},
		"log_level": func(v string) error {
			if oldConfig.LogLevel != v {
				newConfig.LogLevel = v
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "log_level")
			}
			return nil
		},
		"log_rollover": func(v string) error {
			if oldConfig.LogRollover != v {
				newConfig.LogRollover = v
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "log_rollover")
			}
			return nil
		},
		"log_retention_count": func(v string) error {
			count, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("invalid retention count: %w", err)
			}
			if oldConfig.LogRetentionCount != count {
				newConfig.LogRetentionCount = count
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "log_retention_count")
			}
			return nil
		},
		"site_name": func(v string) error {
			newConfig.SiteName = v
			return nil
		},
		"current_theme": func(v string) error {
			newConfig.CurrentTheme = v
			return nil
		},
		"image_directory": func(v string) error {
			if oldConfig.ImageDirectory != v {
				newConfig.ImageDirectory = v
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "image_directory")
			}
			return nil
		},
		"server_compression_enable": func(v string) error {
			enable, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("invalid boolean: %w", err)
			}
			if oldConfig.ServerCompressionEnable != enable {
				newConfig.ServerCompressionEnable = enable
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "server_compression_enable")
			}
			return nil
		},
		"enable_http_cache": func(v string) error {
			enable, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("invalid boolean: %w", err)
			}
			if oldConfig.EnableHTTPCache != enable {
				newConfig.EnableHTTPCache = enable
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "enable_http_cache")
			}
			return nil
		},
		"cache_max_size": func(v string) error {
			size, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid cache max size: %w", err)
			}
			if oldConfig.CacheMaxSize != size {
				newConfig.CacheMaxSize = size
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "cache_max_size")
			}
			return nil
		},
		"cache_max_entry_size": func(v string) error {
			size, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid cache max entry size: %w", err)
			}
			if oldConfig.CacheMaxEntrySize != size {
				newConfig.CacheMaxEntrySize = size
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "cache_max_entry_size")
			}
			return nil
		},
		"cache_max_time": func(v string) error {
			duration, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("invalid cache max time: %w", err)
			}
			if oldConfig.CacheMaxTime != duration {
				newConfig.CacheMaxTime = duration
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "cache_max_time")
			}
			return nil
		},
		"cache_cleanup_interval": func(v string) error {
			duration, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("invalid cache cleanup interval: %w", err)
			}
			if oldConfig.CacheCleanupInterval != duration {
				newConfig.CacheCleanupInterval = duration
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "cache_cleanup_interval")
			}
			return nil
		},
		"db_max_pool_size": func(v string) error {
			size, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("invalid db max pool size: %w", err)
			}
			if oldConfig.DBMaxPoolSize != size {
				newConfig.DBMaxPoolSize = size
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "db_max_pool_size")
			}
			return nil
		},
		"db_min_idle_connections": func(v string) error {
			count, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("invalid db min idle connections: %w", err)
			}
			if oldConfig.DBMinIdleConnections != count {
				newConfig.DBMinIdleConnections = count
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "db_min_idle_connections")
			}
			return nil
		},
		"db_optimize_interval": func(v string) error {
			duration, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("invalid db optimize interval: %w", err)
			}
			if oldConfig.DBOptimizeInterval != duration {
				newConfig.DBOptimizeInterval = duration
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "db_optimize_interval")
			}
			return nil
		},
		"worker_pool_max": func(v string) error {
			max, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("invalid worker pool max: %w", err)
			}
			if oldConfig.WorkerPoolMax != max {
				newConfig.WorkerPoolMax = max
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "worker_pool_max")
			}
			return nil
		},
		"worker_pool_min_idle": func(v string) error {
			min, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("invalid worker pool min idle: %w", err)
			}
			if oldConfig.WorkerPoolMinIdle != min {
				newConfig.WorkerPoolMinIdle = min
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "worker_pool_min_idle")
			}
			return nil
		},
		"worker_pool_max_idle_time": func(v string) error {
			duration, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("invalid worker pool max idle time: %w", err)
			}
			if oldConfig.WorkerPoolMaxIdleTime != duration {
				newConfig.WorkerPoolMaxIdleTime = duration
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "worker_pool_max_idle_time")
			}
			return nil
		},
		"queue_size": func(v string) error {
			size, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("invalid queue size: %w", err)
			}
			if oldConfig.QueueSize != size {
				newConfig.QueueSize = size
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "queue_size")
			}
			return nil
		},
		"session_max_age": func(v string) error {
			age, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("invalid session max age: %w", err)
			}
			if oldConfig.SessionMaxAge != age {
				newConfig.SessionMaxAge = age
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "session_max_age")
			}
			return nil
		},
		"session_http_only": func(v string) error {
			httpOnly, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("invalid session http only: %w", err)
			}
			if oldConfig.SessionHttpOnly != httpOnly {
				newConfig.SessionHttpOnly = httpOnly
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "session_http_only")
			}
			return nil
		},
		"session_secure": func(v string) error {
			secure, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("invalid session secure: %w", err)
			}
			if oldConfig.SessionSecure != secure {
				newConfig.SessionSecure = secure
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "session_secure")
			}
			return nil
		},
		"session_same_site": func(v string) error {
			if oldConfig.SessionSameSite != v {
				newConfig.SessionSameSite = v
				restartRequired = true
				restartRequiredKeys = append(restartRequiredKeys, "session_same_site")
			}
			return nil
		},
		"enable_cache_preload": func(v string) error {
			enable, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("invalid boolean: %w", err)
			}
			newConfig.EnableCachePreload = enable
			if h.SetPreloadEnabled != nil {
				h.SetPreloadEnabled(enable)
			}
			return nil
		},
		"run_file_discovery": func(v string) error {
			enable, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("invalid boolean: %w", err)
			}
			newConfig.RunFileDiscovery = enable
			return nil
		},
	}

	// Process config fields
	isCheckboxField := func(k string) bool {
		return k == "server_compression_enable" || k == "enable_http_cache" ||
			k == "enable_cache_preload" ||
			k == "session_http_only" || k == "session_secure" || k == "run_file_discovery"
	}

	// Check what type of form submission this is
	hasThemes := false
	hasOtherConfigFields := false
	for key := range r.Form {
		if key == "themes" {
			hasThemes = true
		} else if key != "csrf_token" {
			hasOtherConfigFields = true
		}
	}
	// Process unchecked checkboxes unless this is a themes-only update
	// (themes-only means has themes and no other config fields)
	isThemesOnlyUpdate := hasThemes && !hasOtherConfigFields
	shouldProcessUncheckedCheckboxes := !isThemesOnlyUpdate

	for key, setter := range configKeys {
		_, inForm := r.Form[key]

		if !inForm {
			if isCheckboxField(key) && shouldProcessUncheckedCheckboxes {
				if err := setter("false"); err != nil {
					validationErrors[key] = err.Error()
				}
			}
			continue
		}

		value := r.FormValue(key)
		if isCheckboxField(key) {
			if value == "on" {
				value = "true"
			} else {
				value = "false"
			}
		}
		if err := setter(value); err != nil {
			validationErrors[key] = err.Error()
		}
	}

	// Handle themes - themes can be changed without restart
	if themes, ok := r.Form["themes"]; ok && len(themes) > 0 {
		newConfig.Themes = themes
		if newConfig.CurrentTheme != "" {
			found := slices.Contains(themes, newConfig.CurrentTheme)
			if !found && len(themes) > 0 {
				newConfig.CurrentTheme = themes[0]
			}
		}
	}

	// Validate config
	if err := h.ConfigService.Validate(&newConfig); err != nil {
		validationErrors["_global"] = err.Error()
	}

	if len(validationErrors) > 0 {
		w.WriteHeader(http.StatusOK)
		if err := ui.RenderTemplate(w, "config-validation-error.html.tmpl", map[string]any{
			"Errors": validationErrors,
		}); err != nil {
			slog.Error("failed to render validation error template", "err", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Save via ConfigService
	if err := h.ConfigService.Save(h.Ctx, &newConfig); err != nil {
		slog.Error("failed to save config to database", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		if err := ui.RenderTemplate(w, "config-database-error.html.tmpl", nil); err != nil {
			slog.Error("failed to render database error template", "err", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	if h.UpdateConfig != nil {
		h.UpdateConfig(&newConfig, restartRequiredKeys)
	}
	if h.ApplyConfig != nil {
		h.ApplyConfig()
	}

	w.Header().Set("HX-Trigger", "config-saved")

	if restartRequired {
		h.executeConfigTemplate(w, h.Templates.SaveRestartAlert, "config-save-restart-alert.html.tmpl", nil)
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusOK)
	h.executeConfigTemplate(w, h.Templates.SaveSuccessAlert, "config-save-success-alert.html.tmpl", nil)
}

// ExportConfigToFileHandler handles POST /config/export/to-file and returns the
// current configuration in YAML format wrapped in an HTML modal for display.
// This is typically called via HTMX when the user clicks 'Export to Screen'.
// Authentication is required.
func (h *ConfigHandlers) ExportConfigToFileHandler(w http.ResponseWriter, r *http.Request) {
	h.disableConfigCaching(w)
	if !h.SessionManager.IsAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	currentYAML, err := h.ConfigService.Export()
	if err != nil {
		slog.Error("failed to export current config", "err", err)
		http.Error(w, "Failed to export configuration", http.StatusInternalServerError)
		return
	}

	data := struct {
		CurrentYAML string
	}{
		CurrentYAML: html.EscapeString(currentYAML),
	}
	w.WriteHeader(http.StatusOK)
	h.executeConfigTemplate(w, h.Templates.ExportModal, "config-export-modal.html.tmpl", data)
}

// ExportConfigDownloadHandler handles GET /config/export/download and triggers
// a file download of the current configuration in YAML format.
// It sets the Content-Disposition header to 'attachment; filename=config.yaml'.
// Authentication is required.
func (h *ConfigHandlers) ExportConfigDownloadHandler(w http.ResponseWriter, r *http.Request) {
	h.disableConfigCaching(w)
	if !h.SessionManager.IsAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	yamlContent, err := h.ConfigService.Export()
	if err != nil {
		slog.Error("failed to export config", "err", err)
		http.Error(w, "Failed to export configuration", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", "attachment; filename=config.yaml")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(yamlContent))
}

// ImportConfigPreviewHandler handles POST /config/import/preview requests.
// It parses the uploaded YAML config (either via file upload or text area),
// calculates the diff against current config, and returns a preview modal.
// Response: HTML modal (bufferable, caching disabled).
// Authentication and CSRF protection are required.
func (h *ConfigHandlers) ImportConfigPreviewHandler(w http.ResponseWriter, r *http.Request) {
	h.disableConfigCaching(w)
	if !h.SessionManager.IsAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var yamlContent string
	var err error

	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if parseErr := r.ParseMultipartForm(10 << 20); parseErr != nil {
			slog.Warn("failed to parse multipart form", "err", parseErr)
			http.Error(w, "Invalid form data", http.StatusBadRequest)
			return
		}

		file, header, formErr := r.FormFile("yaml")
		if formErr != nil {
			slog.Warn("failed to get file from form", "err", formErr)
			http.Error(w, "YAML file is required", http.StatusBadRequest)
			return
		}
		defer file.Close()

		filename := header.Filename
		if !strings.HasSuffix(strings.ToLower(filename), ".yaml") && !strings.HasSuffix(strings.ToLower(filename), ".yml") {
			http.Error(w, "File must have .yaml or .yml extension", http.StatusBadRequest)
			return
		}

		contentBytes, readErr := io.ReadAll(file)
		if readErr != nil {
			slog.Warn("failed to read file content", "err", readErr)
			http.Error(w, "Failed to read file", http.StatusBadRequest)
			return
		}
		yamlContent = string(contentBytes)
	} else {
		if parseFormErr := r.ParseForm(); parseFormErr != nil {
			http.Error(w, "Invalid form data", http.StatusBadRequest)
			return
		}

		yamlContent = r.FormValue("yaml")
		if yamlContent == "" {
			http.Error(w, "YAML content is required", http.StatusBadRequest)
			return
		}
	}

	if yamlContent == "" {
		http.Error(w, "YAML content is required", http.StatusBadRequest)
		return
	}

	// Load current config to call PreviewImport
	cfg, err := h.ConfigService.Load(h.Ctx)
	if err != nil {
		slog.Error("failed to load current config for preview", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	diff, err := cfg.PreviewImport(yamlContent)
	if err != nil {
		slog.Warn("failed to preview import", "err", err)
		http.Error(w, fmt.Sprintf("Invalid YAML: %v", err), http.StatusBadRequest)
		return
	}

	escapedYaml := html.EscapeString(yamlContent)
	csrfToken := h.ensureCsrf(w, r)

	data := struct {
		ImportedYAML string
		CSRFToken    string
		CurrentYAML  string
		NewYAML      string
	}{
		ImportedYAML: escapedYaml,
		CSRFToken:    html.EscapeString(csrfToken),
		CurrentYAML:  html.EscapeString(diff.CurrentYAML),
		NewYAML:      html.EscapeString(diff.NewYAML),
	}
	w.WriteHeader(http.StatusOK)
	h.executeConfigTemplate(w, h.Templates.ImportModal, "config-import-modal.html.tmpl", data)
}

// ImportConfigCommitHandler handles POST /config/import/commit requests.
// It applies the imported YAML configuration to the system.
// Response: HTML success alert (bufferable, caching disabled).
// Authentication and CSRF protection are required.
func (h *ConfigHandlers) ImportConfigCommitHandler(w http.ResponseWriter, r *http.Request) {
	h.disableConfigCaching(w)
	if !h.SessionManager.IsAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if !h.validateCsrf(r) {
		http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	yamlContent := r.FormValue("yaml")
	if yamlContent == "" {
		http.Error(w, "YAML content is required", http.StatusBadRequest)
		return
	}

	// Import via ConfigService
	if err := h.ConfigService.Import(yamlContent, h.Ctx); err != nil {
		slog.Warn("failed to import config", "err", err)
		http.Error(w, fmt.Sprintf("Import failed: %v", err), http.StatusBadRequest)
		return
	}

	loaded, err := h.ConfigService.Load(h.Ctx)
	if err != nil {
		slog.Warn("failed to load config after import", "err", err)
		http.Error(w, "Import succeeded but failed to load config", http.StatusInternalServerError)
		return
	}
	if h.UpdateConfig != nil {
		h.UpdateConfig(loaded, nil)
	}
	if h.ApplyConfig != nil {
		h.ApplyConfig()
	}

	w.WriteHeader(http.StatusOK)
	h.executeConfigTemplate(w, h.Templates.ImportSuccessAlert, "config-import-success-alert.html.tmpl", nil)
}

// RestoreLastKnownGoodHandler handles POST /config/restore-last-known-good requests.
// Supports 'preview' (returns diff modal) and 'commit' (restores previous config)
// actions via query parameter.
// Response: HTML modal or success alert (bufferable, caching disabled).
// Authentication and CSRF protection are required.
func (h *ConfigHandlers) RestoreLastKnownGoodHandler(w http.ResponseWriter, r *http.Request) {
	h.disableConfigCaching(w)
	if !h.SessionManager.IsAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	action := r.URL.Query().Get("action")
	if action == "preview" || action == "" {
		// Preview: return diff
		cpcRw, err := h.DBRwPool.Get()
		if err != nil {
			slog.Error("failed to get db connection", "err", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		defer h.DBRwPool.Put(cpcRw)

		// Load current config to call GetLastKnownGoodDiff
		cfg, err := h.ConfigService.Load(h.Ctx)
		if err != nil {
			slog.Error("failed to load current config", "err", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// GetLastKnownGoodDiff needs queries - cpcRw.Queries implements ConfigQueries interface
		diff, err := cfg.GetLastKnownGoodDiff(h.Ctx, cpcRw.Queries)
		if err != nil {
			slog.Warn("failed to get last known good diff", "err", err)
			http.Error(w, fmt.Sprintf("Failed to get last known good config: %v", err), http.StatusBadRequest)
			return
		}

		data := struct {
			BackupYAML  string
			CSRFToken   string
			CurrentYAML string
		}{
			BackupYAML:  html.EscapeString(diff.NewYAML),
			CSRFToken:   html.EscapeString(h.ensureCsrf(w, r)),
			CurrentYAML: html.EscapeString(diff.CurrentYAML),
		}
		w.WriteHeader(http.StatusOK)
		h.executeConfigTemplate(w, h.Templates.RestoreModal, "config-restore-modal.html.tmpl", data)
		return
	}

	if action != "commit" {
		http.Error(w, "Invalid action", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if !h.validateCsrf(r) {
		slog.Warn("CSRF validation failed for restore last known good", "remote_addr", r.RemoteAddr)
		http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
		return
	}

	// Restore via ConfigService
	restoredConfig, err := h.ConfigService.RestoreLastKnownGood(h.Ctx)
	if err != nil {
		slog.Warn("failed to restore last known good", "err", err)
		http.Error(w, fmt.Sprintf("Failed to restore last known good config: %v", err), http.StatusBadRequest)
		return
	}

	// Validate restored config
	if validateErr := h.ConfigService.Validate(restoredConfig); validateErr != nil {
		slog.Warn("restored config is invalid", "err", validateErr)
		http.Error(w, fmt.Sprintf("Restored config is invalid: %v", validateErr), http.StatusBadRequest)
		return
	}

	// Save restored config
	if saveErr := h.ConfigService.Save(h.Ctx, restoredConfig); saveErr != nil {
		slog.Error("failed to save restored config", "err", saveErr)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if h.UpdateConfig != nil {
		h.UpdateConfig(restoredConfig, nil)
	}
	if h.ApplyConfig != nil {
		h.ApplyConfig()
	}

	// Check if restart is required
	restartRequired := false
	currentConfig, err := h.ConfigService.Load(h.Ctx)
	if err == nil {
		if currentConfig.ListenerAddress != restoredConfig.ListenerAddress ||
			currentConfig.ListenerPort != restoredConfig.ListenerPort {
			restartRequired = true
		}
	}

	if restartRequired && h.SetRestartRequired != nil {
		h.SetRestartRequired(true)
	}

	data := struct {
		RestartRequired bool
	}{
		RestartRequired: restartRequired,
	}
	w.WriteHeader(http.StatusOK)
	h.executeConfigTemplate(w, h.Templates.RestoreSuccessAlert, "config-restore-success-alert.html.tmpl", data)
}

// RestartHandler handles POST /config/restart requests.
// It initiates an asynchronous application restart.
// Response: HTML alert (bufferable, caching disabled).
// Authentication and CSRF protection are required.
func (h *ConfigHandlers) RestartHandler(w http.ResponseWriter, r *http.Request) {
	h.disableConfigCaching(w)

	if !h.SessionManager.IsAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if !h.validateCsrf(r) {
		slog.Warn("CSRF validation failed for restart", "remote_addr", r.RemoteAddr)
		http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
		return
	}

	w.WriteHeader(http.StatusOK)
	h.executeConfigTemplate(w, h.Templates.RestartInitiatedAlert, "config-restart-initiated-alert.html.tmpl", nil)

	// Trigger restart
	go func() {
		time.Sleep(500 * time.Millisecond)
		slog.Info("Restart requested via web interface, sending restart signal")

		restartCh := h.GetRestartCh()
		if restartCh != nil {
			select {
			case restartCh <- struct{}{}:
				slog.Info("Restart signal sent successfully")
			default:
				slog.Warn("Restart channel full, restart already pending")
			}
		} else {
			slog.Error("Restart channel not initialized")
		}
	}()
}

// ConfigIncrementETag increments the application ETag version.
// POST /config/increment-etag
func (h *ConfigHandlers) ConfigIncrementETag(w http.ResponseWriter, r *http.Request) {
	h.disableConfigCaching(w)

	// Check authentication
	if !h.SessionManager.IsAuthenticated(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse form to get CSRF token
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Validate CSRF token
	if !h.validateCsrf(r) {
		http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
		return
	}

	// Increment ETag version using wired service/logic
	_, err := h.ConfigService.IncrementETag(h.Ctx)
	if err != nil {
		slog.Error("failed to increment etag version", "err", err)
		w.Header().Set("HX-Retarget", "#config-error-message")
		w.Header().Set("HX-Swap", "outerHTML")
		w.WriteHeader(http.StatusInternalServerError)
		ui.RenderTemplate(w, "config-generic-error.html.tmpl", map[string]any{
			"Error": "Failed to increment ETag version",
		})
		return
	}

	// Reload config to get the updated ETag and update in-memory state
	cfg, err := h.ConfigService.Load(h.Ctx)
	if err != nil {
		slog.Error("failed to reload config after etag increment", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Update in-memory app state
	if h.UpdateConfig != nil {
		h.UpdateConfig(cfg, nil)
	}

	// Update UI-wide cache version
	ui.SetCacheVersion(cfg.ETagVersion)

	// Invalidate HTTP cache so stale responses with old cache-busting URLs are not served.
	if h.InvalidateHTTPCache != nil {
		h.InvalidateHTTPCache()
	}

	slog.Info("etag version incremented and app config updated",
		"new", cfg.ETagVersion)

	// Return updated field (HTMX will swap this into the page)
	data := map[string]any{
		"ETagVersion": cfg.ETagVersion,
	}

	w.WriteHeader(http.StatusOK)
	ui.RenderTemplate(w, "config-etag-field.html.tmpl", data)
}
