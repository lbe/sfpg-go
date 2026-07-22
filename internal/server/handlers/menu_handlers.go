package handlers

import (
	"log/slog"
	"net/http"

	"github.com/lbe/sfpg-go/internal/server/ui"
)

// MenuHandlers holds dependencies for the hamburger menu endpoint.
type MenuHandlers struct {
	sessionManager SessionManager
	ServerError    func(w http.ResponseWriter, r *http.Request, err error)
}

// NewMenuHandlers creates a new MenuHandlers with the given dependencies.
func NewMenuHandlers(
	sessionManager SessionManager,
	serverError func(w http.ResponseWriter, r *http.Request, err error),
) *MenuHandlers {
	return &MenuHandlers{
		sessionManager: sessionManager,
		ServerError:    serverError,
	}
}

// HamburgerMenu handles GET /hamburger-menu, rendering the hamburger menu items
// based on the current session's authentication state.
//
// This endpoint is uncached (Cache-Control: no-store) because the menu content
// depends on the auth state, which changes dynamically. The response contains
// only <li> elements (no <ul> wrapper) for use with HTMX innerHTML swap into
// the persistent <ul id="hamburger-menu-items"> element in the layout.
func (h *MenuHandlers) HamburgerMenu(w http.ResponseWriter, r *http.Request) {
	authenticated := h.sessionManager.IsAuthenticated(w, r)

	data := map[string]any{
		"IsAuthenticated": authenticated,
		// cacheVersion is resolved via FuncMap (GetCacheVersion()), not from template data
	}

	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	if err := ui.RenderTemplate(w, "hamburger-menu-items.html.tmpl", data); err != nil {
		slog.Error("failed to render hamburger menu", "err", err)
		h.ServerError(w, r, err)
	}
}
