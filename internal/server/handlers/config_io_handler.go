package handlers

import (
	"errors"
	"html"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/lbe/sfpg-go/internal/server/config"
)

// ExportConfigToFileHandler handles POST /config/export/to-file and returns the
// current configuration in YAML format wrapped in an HTML modal for display.
// This is typically called via HTMX when the user clicks 'Export to Screen'.
// Authentication is required.
func (h *ConfigHandlers) ExportConfigToFileHandler(w http.ResponseWriter, r *http.Request) {
	currentYAML, err := h.ConfigService.Export(r.Context())
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
	yamlContent, err := h.ConfigService.Export(r.Context())
	if err != nil {
		slog.Error("failed to export config", "err", err)
		http.Error(w, "Failed to export configuration", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-yaml")
	w.Header().Set("Content-Disposition", "attachment; filename=config.yaml")
	w.WriteHeader(http.StatusOK)
	if _, wrErr := w.Write([]byte(yamlContent)); wrErr != nil {
		slog.Error("failed to write yaml content in response", "err", wrErr)
	}
}

// ImportConfigPreviewHandler handles POST /config/import/preview requests.
// It parses the uploaded YAML config (either via file upload or text area),
// calculates the diff against current config, and returns a preview modal.
// Response: HTML modal (bufferable, caching disabled).
// Authentication and CSRF protection are required.
func (h *ConfigHandlers) ImportConfigPreviewHandler(w http.ResponseWriter, r *http.Request) {
	if !h.validateCsrf(r) {
		slog.Warn("CSRF validation failed for config import preview", "remote_addr", r.RemoteAddr)
		http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
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
			slog.Warn("failed to parse form", "err", parseFormErr)
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
		http.Error(w, "Invalid YAML content", http.StatusBadRequest)
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
	if !h.validateCsrf(r) {
		slog.Warn("CSRF validation failed for config import commit", "remote_addr", r.RemoteAddr)
		http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		slog.Warn("failed to parse form for config import", "err", err)
		http.Error(w, "Invalid form data", http.StatusBadRequest)
		return
	}

	yamlContent := r.FormValue("yaml")
	if yamlContent == "" {
		http.Error(w, "YAML content is required", http.StatusBadRequest)
		return
	}

	oldConfig, err := h.ConfigService.Load(h.Ctx)
	if err != nil {
		slog.Warn("failed to load current config for import", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	importedConfig, err := config.BuildImportedConfig(oldConfig, yamlContent)
	if err != nil {
		slog.Warn("failed to import config", "err", err)
		http.Error(w, "Import failed", http.StatusBadRequest)
		return
	}

	applyResult, err := config.ApplyConfig(h.Ctx, h.ConfigService, oldConfig, importedConfig)
	if err != nil {
		var validationErr *config.ApplyValidationError
		if errors.As(err, &validationErr) {
			http.Error(w, "Import failed", http.StatusBadRequest)
			return
		}

		slog.Warn("failed to apply imported config", "err", err)
		http.Error(w, "Import failed: unable to persist config", http.StatusInternalServerError)
		return
	}

	h.cfgOps.UpdateConfigWithPrecedence(applyResult.Config, applyResult.RestartRequiredKeys)
	h.cfgOps.ApplyConfig()
	if applyResult.RestartRequired {
		h.cfgOps.SetRestartRequired(true)
	}

	w.WriteHeader(http.StatusOK)
	if applyResult.RestartRequired {
		h.executeConfigTemplate(w, h.Templates.SaveRestartAlert, "config-save-restart-alert.html.tmpl", nil)
		return
	}
	h.executeConfigTemplate(w, h.Templates.ImportSuccessAlert, "config-import-success-alert.html.tmpl", nil)
}

// RestoreLastKnownGoodHandler handles POST /config/restore-last-known-good requests.
// Supports 'preview' (returns diff modal) and 'commit' (restores previous config)
// actions via query parameter.
// Response: HTML modal or success alert (bufferable, caching disabled).
// Authentication and CSRF protection are required.
func (h *ConfigHandlers) RestoreLastKnownGoodHandler(w http.ResponseWriter, r *http.Request) {
	action := r.URL.Query().Get("action")
	if action == "preview" || action == "" {
		if !h.validateCsrf(r) {
			slog.Warn("CSRF validation failed for config restore preview", "remote_addr", r.RemoteAddr)
			http.Error(w, "Forbidden - CSRF token invalid", http.StatusForbidden)
			return
		}

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
		diff, err := cfg.GetLastKnownGoodDiff(h.Ctx, h.getConfigQueriesFn(cpcRw))
		if err != nil {
			slog.Warn("failed to get last known good diff", "err", err)
			http.Error(w, "Failed to get last known good config", http.StatusBadRequest)
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

	currentConfig, err := h.ConfigService.Load(h.Ctx)
	if err != nil {
		slog.Warn("failed to load current config before restore", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Restore via ConfigService
	restoredConfig, err := h.ConfigService.RestoreLastKnownGood(h.Ctx)
	if err != nil {
		slog.Warn("failed to restore last known good", "err", err)
		http.Error(w, "Failed to restore last known good config", http.StatusBadRequest)
		return
	}

	applyResult, err := config.ApplyConfig(h.Ctx, h.ConfigService, currentConfig, restoredConfig)
	if err != nil {
		var validationErr *config.ApplyValidationError
		if errors.As(err, &validationErr) {
			slog.Warn("restored config is invalid", "err", err)
			http.Error(w, "Restored config is invalid", http.StatusBadRequest)
			return
		}

		slog.Error("failed to save restored config", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	h.cfgOps.UpdateConfigWithPrecedence(applyResult.Config, applyResult.RestartRequiredKeys)
	h.cfgOps.ApplyConfig()
	restartRequired := applyResult.RestartRequired

	if restartRequired {
		h.cfgOps.SetRestartRequired(true)
	}

	data := struct {
		RestartRequired bool
	}{
		RestartRequired: restartRequired,
	}
	w.WriteHeader(http.StatusOK)
	h.executeConfigTemplate(w, h.Templates.RestoreSuccessAlert, "config-restore-success-alert.html.tmpl", data)
}
