package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"maps"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/lbe/sfpg-go/internal/dbconnpool"
	"github.com/lbe/sfpg-go/internal/server/auth"
	"github.com/lbe/sfpg-go/internal/server/config"
	"github.com/lbe/sfpg-go/internal/server/session"
	"github.com/lbe/sfpg-go/internal/server/ui"
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
	InvalidateHTTPCache func()
	SetPreloadEnabled   func(bool)
	SetRestartRequired  func(bool)
	GetRestartCh        func() chan struct{}

	// Helpers
	AddCommonTemplateData func(http.ResponseWriter, *http.Request, map[string]any, bool) map[string]any
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

// ConfigAuthMiddleware wraps config handlers with cache-disabling headers.
// Authentication is enforced by the router-level authMiddleware, so per-handler
// IsAuthenticated checks are not needed. CSRF validation remains handler-specific
// because the restore-preview action is a POST that does not require CSRF.
func (h *ConfigHandlers) ConfigAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h.disableConfigCaching(w)
		next(w, r)
	}
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

	// Help text and example values are already populated by LoadFromDatabase
	// during ConfigService.Load(). Read them from the loaded config.
	data["HelpText"] = cfg.HelpText
	data["ExampleValue"] = cfg.ExampleValues

	// Check for category query parameter
	category := r.URL.Query().Get("category")
	if category != "" {
		data["Category"] = category
	}

	data = h.AddCommonTemplateData(w, r, data, true)

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

	// Check if the form contains actual config fields beyond csrf_token.
	// Only default unchecked checkboxes to false for genuine config updates,
	// not for partial or empty submissions (e.g. themes-only or bare csrf).
	hasConfigFields := false
	for key := range r.Form {
		if key != "csrf_token" {
			hasConfigFields = true
			break
		}
	}

	// Process config fields by iterating the single source of truth.
	// Side effects (e.g. cache preload) are handled inline.
	for _, f := range config.Fields() {
		_, inForm := r.Form[f.DBKey]

		var value string
		if !inForm {
			if f.IsCheckbox && hasConfigFields {
				value = "false"
			} else {
				continue
			}
		} else {
			// Themes is a multi-select: collect all values and encode as JSON.
			if f.DBKey == "themes" {
				if themeValues, ok := r.Form[f.DBKey]; ok {
					var b []byte
					b, err = json.Marshal(themeValues)
					if err != nil {
						validationErrors[f.DBKey] = err.Error()
						continue
					}
					value = string(b)
				} else {
					continue
				}
			} else {
				value = r.FormValue(f.DBKey)
			}
			if f.IsCheckbox {
				if value == "on" {
					value = "true"
				} else {
					value = "false"
				}
			}
		}

		if setErr := f.Set(&newConfig, value); setErr != nil {
			validationErrors[f.DBKey] = setErr.Error()
		}

		// Inline the former configFieldSetter sideEffect for cache preload.
		if f.DBKey == "enable_cache_preload" {
			if h.SetPreloadEnabled != nil {
				h.SetPreloadEnabled(value == "true")
			}
		}
	}

	// Reject directory-traversal paths; validate path existence and readability
	// for changed image directories before persisting any config changes.
	// Note: the directory must already exist on disk; previously a missing
	// directory would be created lazily by ApplyImageDirectory, but now an
	// invalid or missing path is rejected at save time.
	if newConfig.ImageDirectory != oldConfig.ImageDirectory && newConfig.ImageDirectory != "" {
		cleanDir := filepath.Clean(newConfig.ImageDirectory)
		if strings.HasPrefix(cleanDir, "..") {
			validationErrors["image_directory"] = fmt.Sprintf("image directory %q is outside the application root", newConfig.ImageDirectory)
		} else if validateDirErr := config.ValidateImageDirectory(newConfig.ImageDirectory); validateDirErr != nil {
			validationErrors["image_directory"] = validateDirErr.Error()
		}
	}

	if len(validationErrors) > 0 {
		w.WriteHeader(http.StatusOK)
		if renderErr := ui.RenderTemplate(w, "config-validation-error.html.tmpl", map[string]any{
			"Errors": validationErrors,
		}); renderErr != nil {
			slog.Error("failed to render validation error template", "err", renderErr)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	applyResult, err := config.ApplyConfig(h.Ctx, h.ConfigService, oldConfig, &newConfig)
	if err != nil {
		var validationErr *config.ApplyValidationError
		if errors.As(err, &validationErr) {
			w.WriteHeader(http.StatusOK)
			if renderErr := ui.RenderTemplate(w, "config-validation-error.html.tmpl", map[string]any{
				"Errors": map[string]string{"_global": validationErr.Error()},
			}); renderErr != nil {
				slog.Error("failed to render validation error template", "err", renderErr)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
			return
		}

		slog.Error("failed to save config to database", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		if err := ui.RenderTemplate(w, "config-database-error.html.tmpl", nil); err != nil {
			slog.Error("failed to render database error template", "err", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	if h.UpdateConfig != nil {
		h.UpdateConfig(applyResult.Config, applyResult.RestartRequiredKeys)
	}
	if h.ApplyConfig != nil {
		h.ApplyConfig()
	}

	// Set restart required flag if any restart-required fields changed
	if h.SetRestartRequired != nil && applyResult.RestartRequired {
		h.SetRestartRequired(true)
	}

	w.Header().Set("HX-Trigger", "config-saved")

	if applyResult.RestartRequired {
		h.executeConfigTemplate(w, h.Templates.SaveRestartAlert, "config-save-restart-alert.html.tmpl", nil)
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusOK)
	h.executeConfigTemplate(w, h.Templates.SaveSuccessAlert, "config-save-success-alert.html.tmpl", nil)
}
