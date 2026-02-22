// Package ui provides HTML template rendering and cache version management
// for the application's user interface. It manages template initialization,
// cache busting, and page rendering functions.
package ui

import (
	"fmt"
	"html/template"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lbe/sfpg-go/internal/humanize"
	"github.com/lbe/sfpg-go/internal/server/metrics"
)

var (
	cacheVersionMu sync.RWMutex
	cacheVersion   string
)

// SetCacheVersion updates the application-wide cache version.
// This should be called when config is loaded or updated.
func SetCacheVersion(version string) {
	cacheVersionMu.Lock()
	defer cacheVersionMu.Unlock()
	cacheVersion = version
}

// GetCacheVersion returns the current cache version (thread-safe).
func GetCacheVersion() string {
	cacheVersionMu.RLock()
	defer cacheVersionMu.RUnlock()
	return cacheVersion
}

// Pre-parsed HTML templates for various application pages and partials.
var (
	loginFormTemplate               *template.Template
	galleryTemplate                 *template.Template
	configModalTemplate             *template.Template
	imageTemplate                   *template.Template
	galleryPartialTemplate          *template.Template
	galleryOOBTemplate              *template.Template
	lightboxContentTemplate         *template.Template
	configSuccessTemplate           *template.Template
	adminCredentialsSuccessTemplate *template.Template
	configValidationErrorTemplate   *template.Template
	configGenericErrorTemplate      *template.Template
	configDatabaseErrorTemplate     *template.Template
	infoBoxFolderTemplate           *template.Template
	infoBoxImageTemplate            *template.Template
	configEtagFieldTemplate         *template.Template
	hamburgerMenuItemsTemplate      *template.Template
	dashboardTemplate               *template.Template
	dashboardPartialTemplate        *template.Template
	serverShutdownTemplate          *template.Template
	discoveryStartedTemplate        *template.Template
	shutdownModalTemplate           *template.Template
	themeModalTemplate              *template.Template
	funcMap                         template.FuncMap
)

// basenameWithoutExt extracts the filename from a path and removes its extension.
func basenameWithoutExt(fullPath string) string {
	fileNameWithExt := filepath.Base(fullPath)
	ext := filepath.Ext(fileNameWithExt)
	if ext == "" {
		return fileNameWithExt
	}
	return fileNameWithExt[:len(fileNameWithExt)-len(ext)]
}

// EscapeHash replaces all '#' characters with '%23' for use in safe URL query parameters.
// Exported for use in tests and template funcMap.
func EscapeHash(s string) string {
	return strings.ReplaceAll(s, "#", "%23")
}

// ParseTemplates parses all embedded HTML templates and the associated function map.
// It uses GetCacheVersion() for cache-busting in templates.
func ParseTemplates(templateFS fs.FS) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("templates panic: %v", r)
		}
	}()

	funcMap = template.FuncMap{
		"basename":           filepath.Base,
		"basenameWithoutExt": basenameWithoutExt,
		"plus": func(a, b int) int {
			return a + b
		},
		"sub": func(a, b int) int {
			return a - b
		},
		"escapeHash": EscapeHash,
		"formatUnix": func(ts int64) string {
			return time.Unix(ts, 0).Format(time.ANSIC)
		},
		"formatInt": func(n int64) string {
			return humanize.Comma(n).String()
		},
		"formatCount": func(v any) string {
			switch n := v.(type) {
			case int:
				return humanize.Comma(n).String()
			case int8:
				return humanize.Comma(n).String()
			case int16:
				return humanize.Comma(n).String()
			case int32:
				return humanize.Comma(n).String()
			case int64:
				return humanize.Comma(n).String()
			case uint:
				return humanize.Comma(n).String()
			case uint8:
				return humanize.Comma(n).String()
			case uint16:
				return humanize.Comma(n).String()
			case uint32:
				return humanize.Comma(n).String()
			case uint64:
				return humanize.Comma(n).String()
			case float32:
				return humanize.Comma(n).String()
			case float64:
				return humanize.Comma(n).String()
			default:
				return fmt.Sprintf("%v", v)
			}
		},
		"formatBytes":      metrics.FormatBytes,
		"formatBytesInt64": metrics.FormatBytesInt64,
		"formatDuration":   metrics.FormatDuration,
		"writeBatcherQueuePercent": func(wb metrics.WriteBatcherMetrics) int {
			if wb.ChannelSize == 0 {
				return 0
			}
			return int(float64(wb.PendingCount) / float64(wb.ChannelSize) * 100)
		},
		"queueUtilizationPercent": func(length, capacity int) int {
			if capacity == 0 {
				return 0
			}
			return int(float64(length) / float64(capacity) * 100)
		},
		"queueUtilizationColor": func(length, capacity int) string {
			if capacity == 0 {
				return "progress"
			}
			pct := float64(length) / float64(capacity) * 100
			if pct < 50 {
				return "progress-success"
			} else if pct < 80 {
				return "progress-warning"
			}
			return "progress-error"
		},
		"httpCacheUtilizationPercent": func(cache metrics.HTTPCacheMetrics) int {
			if cache.MaxTotalSize == 0 {
				return 0
			}
			return int(float64(cache.SizeBytes) / float64(cache.MaxTotalSize) * 100)
		},
		"cacheVersion": GetCacheVersion,
	}

	baseTemplates := []string{
		"templates/layout.html.tmpl",
		"templates/login-form.html.tmpl",
		"templates/login-modal.html.tmpl",
		"templates/logout-modal.html.tmpl",
		"templates/shutdown-modal.html.tmpl",
		"templates/about-modal.html.tmpl",
	}
	galleryTemplate = template.Must(template.New("gallery.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, append(baseTemplates, "templates/gallery.html.tmpl")...))
	configModalTemplate = template.Must(template.New("config-modal.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, "templates/config-modal.html.tmpl", "templates/config-etag-field.html.tmpl"))
	imageTemplate = template.Must(template.New("image.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, append(baseTemplates, "templates/image.html.tmpl")...))

	galleryPartialTemplate = galleryTemplate.Lookup("body")
	if galleryPartialTemplate == nil {
		panic("template body not found in galleryTemplate")
	}
	galleryOOBTemplate = galleryTemplate.Lookup("gallery-oob")
	if galleryOOBTemplate == nil {
		panic("template gallery-oob not found in galleryTemplate")
	}

	lightboxContentTemplate = template.Must(template.New("lightbox-content").Funcs(funcMap).
		ParseFS(templateFS, "templates/lightbox-content.html.tmpl"))

	configSuccessTemplate = template.Must(template.New("config-success.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, "templates/config-success.html.tmpl"))

	adminCredentialsSuccessTemplate = template.Must(template.New("admin-credentials-success.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, "templates/admin-credentials-success.html.tmpl"))

	configValidationErrorTemplate = template.Must(template.New("config-validation-error.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, "templates/config-validation-error.html.tmpl"))

	configGenericErrorTemplate = template.Must(template.New("config-generic-error.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, "templates/config-generic-error.html.tmpl"))

	configDatabaseErrorTemplate = template.Must(template.New("config-database-error.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, "templates/config-database-error.html.tmpl"))

	loginFormTemplate = template.Must(template.New("login-form.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, "templates/login-form.html.tmpl"))

	infoBoxFolderTemplate = template.Must(template.New("infobox-folder.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, "templates/infobox-folder.html.tmpl"))

	infoBoxImageTemplate = template.Must(template.New("infobox-image.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, "templates/infobox-image.html.tmpl"))

	configEtagFieldTemplate = template.Must(template.New("config-etag-field.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, "templates/config-etag-field.html.tmpl"))

	hamburgerMenuItemsTemplate = template.Must(template.New("hamburger-menu-items.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, "templates/hamburger-menu-items.html.tmpl"))

	dashboardTemplate = template.Must(template.New("dashboard.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, append(baseTemplates, "templates/dashboard.html.tmpl")...))

	dashboardPartialTemplate = dashboardTemplate.Lookup("body")
	if dashboardPartialTemplate == nil {
		panic("template body not found in dashboardTemplate")
	}

	// Server management templates
	serverShutdownTemplate = template.Must(template.New("server-shutdown.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, append(baseTemplates, "templates/server-shutdown.html.tmpl")...))

	discoveryStartedTemplate = template.Must(template.New("discovery-started.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, "templates/discovery-started.html.tmpl"))

	shutdownModalTemplate = template.Must(template.New("shutdown-modal.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, "templates/shutdown-modal.html.tmpl"))

	themeModalTemplate = template.Must(template.New("theme-modal.html.tmpl").Funcs(funcMap).
		ParseFS(templateFS, "templates/theme-modal.html.tmpl"))

	return nil
}
