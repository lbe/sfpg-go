package ui

import "net/http"

// PartialRule selects how HTMX headers map to ui.RenderPage(..., partial).
type PartialRule int

const (
	// PartialRuleHXRequestOnly: partial when HX-Request is exactly "true" (dashboard).
	PartialRuleHXRequestOnly PartialRule = iota
	// PartialRuleHXRequestTarget: partial when HX-Request is "true" and HX-Target matches wantTarget (gallery).
	PartialRuleHXRequestTarget
)

// HXTargetGalleryContent is the HX-Target value for gallery layout partial swaps.
const HXTargetGalleryContent = "gallery-content"

// HXRequest reports whether the request has HX-Request: true (case-sensitive).
func HXRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// IsPartialRequest reports true when the handler calls RenderPage with partial=true.
// For PartialRuleHXRequestOnly, wantTarget is ignored.
func IsPartialRequest(r *http.Request, rule PartialRule, wantTarget string) bool {
	if !HXRequest(r) {
		return false
	}
	switch rule {
	case PartialRuleHXRequestOnly:
		return true
	case PartialRuleHXRequestTarget:
		return r.Header.Get("HX-Target") == wantTarget
	default:
		return false
	}
}

// SetLayoutPartialVary appends Vary values via Header().Add (not Set), matching current handlers:
// varyTarget false → one Add("Vary", "HX-Request") (dashboard);
// varyTarget true → Add HX-Request then Add HX-Target (gallery).
// Call on the same successful HTML response paths as today (after auth/validation), for full page and layout partial alike — not on 401/error short-circuits.
func SetLayoutPartialVary(w http.ResponseWriter, varyTarget bool) {
	w.Header().Add("Vary", "HX-Request")
	if varyTarget {
		w.Header().Add("Vary", "HX-Target")
	}
}

// SetPartialNoStore sets w.Header().Set("Cache-Control", "no-store") for layout partial responses.
func SetPartialNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}
