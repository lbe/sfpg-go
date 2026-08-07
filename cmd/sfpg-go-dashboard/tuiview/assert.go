// Package tuiview provides test helpers for Bubble Tea / lipgloss rendered views.
package tuiview

import (
	"regexp"
	"strings"
	"testing"
)

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// StripANSI removes terminal color/style sequences from a rendered view.
func StripANSI(s string) string {
	return ansiEscape.ReplaceAllString(s, "")
}

// AssertPlainIncludes fails when want is not present in the ANSI-stripped view.
// Prefer exact equality on full views when output is stable (e.g. quitting/loading).
func AssertPlainIncludes(t *testing.T, view, want string) {
	t.Helper()
	plain := StripANSI(view)
	if !strings.Contains(plain, want) {
		t.Fatalf("rendered view does not include %q (plain text below):\n%s", want, plain)
	}
}
