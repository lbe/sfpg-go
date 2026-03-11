package ui

import (
	"fmt"
	"html/template"
	"io"
	"log/slog"
)

// RenderPage renders a full HTML page by executing a named template within the
// base "layout" template. If partial is true, it renders only the "body"
// block for htmx partial updates.
func RenderPage(w io.Writer, name string, data any, partial bool) error {
	slog.Debug("renderPage called", "name", name, "partial", partial)
	if partial {
		switch name {
		case "gallery":
			if err := galleryPartialTemplate.Execute(w, data); err != nil {
				slog.Error("Error executing partial template", "error", err)
				return err
			}
			return galleryOOBTemplate.Execute(w, data)
		case "dashboard":
			if err := dashboardPartialTemplate.Execute(w, data); err != nil {
				return err
			}
			// Include hamburger menu items to preserve dashboard link on refresh
			return hamburgerMenuItemsTemplate.Execute(w, data)
		default:
			return fmt.Errorf("no partial definition for page: %s", name)
		}
	}

	var t *template.Template
	switch name {
	case "gallery":
		t = galleryTemplate
	case "image":
		t = imageTemplate
	case "dashboard":
		t = dashboardTemplate
	case "shutdown":
		t = serverShutdownTemplate
	case "discovery-started":
		// discovery-started is a standalone notification template
		return discoveryStartedTemplate.Execute(w, data)
	case "cache-batch-load-started":
		return cacheBatchLoadStartedTemplate.Execute(w, data)
	default:
		slog.Error("unknown page", "name", name)
		return nil
	}
	if t == nil {
		slog.Error("template not initialized", "name", name)
		return fmt.Errorf("template not initialized: %s", name)
	}
	return t.ExecuteTemplate(w, "layout", data)
}

// RenderTemplate renders a single, standalone template by name. It is used for
// partials or components that are not part of a full page layout.
func RenderTemplate(w io.Writer, name string, data any) error {
	var t *template.Template
	switch name {
	case "lightbox-content.html.tmpl":
		t = lightboxContentTemplate
	case "config-success.html.tmpl":
		t = configSuccessTemplate
	case "admin-credentials-success.html.tmpl":
		t = adminCredentialsSuccessTemplate
	case "config-validation-error.html.tmpl":
		t = configValidationErrorTemplate
	case "config-generic-error.html.tmpl":
		t = configGenericErrorTemplate
	case "config-database-error.html.tmpl":
		t = configDatabaseErrorTemplate
	case "config-etag-field.html.tmpl":
		t = configEtagFieldTemplate
	case "config-modal.html.tmpl":
		t = configModalTemplate
	case "login-form.html.tmpl":
		t = loginFormTemplate
	case "infobox-folder.html.tmpl":
		t = infoBoxFolderTemplate
	case "infobox-image.html.tmpl":
		t = infoBoxImageTemplate
	case "hamburger-menu-items.html.tmpl":
		t = hamburgerMenuItemsTemplate
	case "theme-modal.html.tmpl":
		t = themeModalTemplate
	default:
		slog.Error("unknown template for renderTemplate", "name", name)
		return fmt.Errorf("unknown template: %s", name)
	}
	err := t.Execute(w, data)
	if err != nil {
		slog.Error("t.ExecuteTemplate failed", "template_name", name, "err", err)
	}
	return err
}
