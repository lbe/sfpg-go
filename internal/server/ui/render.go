package ui

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"log/slog"

	"github.com/lbe/sfpg-go/internal/gensyncpool"
)

const (
	// defaultBufCap is the initial capacity for gallery page render buffers (64 KiB).
	defaultBufCap = 64 << 10 // 64KB

	// maxRetainedBufCap is the maximum buffer capacity retained when returning
	// a buffer to the pool. Larger buffers are replaced with fresh 64 KiB ones.
	maxRetainedBufCap = 256 << 10 // 256KB
)

// galleryBufPool pools *bytes.Buffer for gallery page rendering to reduce
// allocation churn during template execution and single Write output.
var galleryBufPool = gensyncpool.New(
	func() *bytes.Buffer {
		return bytes.NewBuffer(make([]byte, 0, defaultBufCap))
	},
	func(b *bytes.Buffer) {
		if b.Cap() > maxRetainedBufCap {
			*b = *bytes.NewBuffer(make([]byte, 0, defaultBufCap))
			return
		}
		b.Reset()
	},
)

// bodyCaptureWriter is a duck-typed interface that allows gallery render to
// hand off template output into a cache capturer body sink — avoiding an
// unnecessary intermediate buffer copy. If w does not implement this
// interface, renderGalleryInto falls back to the galleryBufPool + single Write.
type bodyCaptureWriter interface {
	CaptureBodyWriter() io.Writer
	CommitCapturedBody() error
	ResetCapturedBody()
}

// renderGalleryInto executes the provided exec function using the optimal
// output strategy for w:
//   - If w implements bodyCaptureWriter, exec writes into a sink that buffers
//     without committing the HTTP status; on success CommitCapturedBody flushes
//     body + status, on error ResetCapturedBody allows the handler to write a
//     500 response.
//   - Otherwise, exec writes into a pooled *bytes.Buffer and a single Write
//     call copies the result to w (fallback path).
func renderGalleryInto(w io.Writer, exec func(io.Writer) error) error {
	if c, ok := w.(bodyCaptureWriter); ok {
		sink := c.CaptureBodyWriter()
		if err := exec(sink); err != nil {
			c.ResetCapturedBody()
			return err
		}
		return c.CommitCapturedBody()
	}
	// fallback: existing galleryBufPool + single Write
	buf := galleryBufPool.Get()
	defer galleryBufPool.Put(buf)
	if err := exec(buf); err != nil {
		return err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// RenderPage renders a full HTML page by executing a named template within the
// base "layout" template. If partial is true, it renders only the "body"
// block for htmx partial updates.
func RenderPage(w io.Writer, name string, data any, partial bool) error {
	parseMu.RLock()
	defer parseMu.RUnlock()

	slog.Debug("renderPage called", "name", name, "partial", partial)
	if partial {
		switch name {
		case "gallery":
			return renderGalleryInto(w, func(sink io.Writer) error {
				if err := galleryPartialTemplate.Execute(sink, data); err != nil {
					slog.Error("Error executing partial template", "error", err)
					return err
				}
				return galleryOOBTemplate.Execute(sink, data)
			})
		case "dashboard":
			if err := dashboardPartialTemplate.Execute(w, data); err != nil {
				return err
			}
			// Menu items are loaded independently via /hamburger-menu
			// (no longer rendered as OOB swap in partial responses)
			return nil
		default:
			return fmt.Errorf("no partial definition for page: %s", name)
		}
	}

	switch name {
	case "gallery":
		if galleryTemplate == nil {
			slog.Error("gallery template not initialized", "name", name)
			return fmt.Errorf("template not initialized: %s", name)
		}
		return renderGalleryInto(w, func(sink io.Writer) error {
			return galleryTemplate.ExecuteTemplate(sink, "layout", data)
		})
	case "image":
		if imageTemplate == nil {
			slog.Error("image template not initialized", "name", name)
			return fmt.Errorf("template not initialized: %s", name)
		}
		return imageTemplate.ExecuteTemplate(w, "layout", data)
	case "dashboard":
		if dashboardTemplate == nil {
			slog.Error("dashboard template not initialized", "name", name)
			return fmt.Errorf("template not initialized: %s", name)
		}
		return dashboardTemplate.ExecuteTemplate(w, "layout", data)
	case "shutdown":
		if serverShutdownTemplate == nil {
			slog.Error("shutdown template not initialized", "name", name)
			return fmt.Errorf("template not initialized: %s", name)
		}
		return serverShutdownTemplate.ExecuteTemplate(w, "layout", data)
	case "discovery-started":
		// discovery-started is a standalone notification template
		return discoveryStartedTemplate.Execute(w, data)
	case "cache-batch-load-started":
		return cacheBatchLoadStartedTemplate.Execute(w, data)
	default:
		slog.Error("unknown page", "name", name)
		return nil
	}
}

// RenderTemplate renders a single, standalone template by name. It is used for
// partials or components that are not part of a full page layout.
func RenderTemplate(w io.Writer, name string, data any) error {
	parseMu.RLock()
	defer parseMu.RUnlock()

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
	case "logout-form-inner.html.tmpl":
		t = logoutFormInnerTemplate
	case "login-form-inner.html.tmpl":
		t = loginFormInnerTemplate
	case "infobox-folder.html.tmpl":
		t = infoBoxFolderTemplate
	case "infobox-image.html.tmpl":
		t = infoBoxImageTemplate
	case "hamburger-menu-items.html.tmpl":
		t = hamburgerMenuItemsTemplate
	case "theme-modal.html.tmpl":
		t = themeModalTemplate
	case "about-modal.html.tmpl":
		t = aboutModalTemplate
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
