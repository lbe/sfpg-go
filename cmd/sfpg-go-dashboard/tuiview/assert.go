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

// AssertPlainExcludes fails when want is present in the ANSI-stripped view.
func AssertPlainExcludes(t *testing.T, view, want string) {
	t.Helper()
	plain := StripANSI(view)
	if strings.Contains(plain, want) {
		t.Fatalf("rendered view includes forbidden %q (plain text below):\n%s", want, plain)
	}
}

// AssertPlainAppearsBefore fails when first or second is missing from the
// ANSI-stripped view, or when first does not appear before second.
func AssertPlainAppearsBefore(t *testing.T, view, first, second string) {
	t.Helper()
	plain := StripANSI(view)
	i1 := strings.Index(plain, first)
	i2 := strings.Index(plain, second)
	if i1 < 0 {
		t.Fatalf("rendered view missing %q (plain text below):\n%s", first, plain)
	}
	if i2 < 0 {
		t.Fatalf("rendered view missing %q (plain text below):\n%s", second, plain)
	}
	if i1 >= i2 {
		t.Fatalf("%q (index %d) does not appear before %q (index %d) (plain text below):\n%s", first, i1, second, i2, plain)
	}
}

// AssertPlainSameRow fails when first and second are not both present on the same
// \n-delimited line in the ANSI-stripped view (lipgloss JoinHorizontal pairing).
func AssertPlainSameRow(t *testing.T, view, first, second string) {
	t.Helper()
	plain := StripANSI(view)
	for line := range strings.SplitSeq(plain, "\n") {
		if strings.Contains(line, first) && strings.Contains(line, second) {
			return
		}
	}
	t.Fatalf("%q and %q are not on the same line (plain text below):\n%s", first, second, plain)
}

// AssertPlainNotSameRow fails when first and second appear on the same \n-delimited line.
func AssertPlainNotSameRow(t *testing.T, view, first, second string) {
	t.Helper()
	plain := StripANSI(view)
	for line := range strings.SplitSeq(plain, "\n") {
		if strings.Contains(line, first) && strings.Contains(line, second) {
			t.Fatalf("%q and %q must not be on the same line (plain text below):\n%s", first, second, plain)
		}
	}
}
