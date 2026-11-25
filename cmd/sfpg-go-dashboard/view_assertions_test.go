package main

import (
	"regexp"
	"strings"
	"testing"
)

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// stripANSI removes terminal color/style sequences from a rendered view.
func stripANSI(s string) string {
	return ansiEscape.ReplaceAllString(s, "")
}

// assertPlainIncludes fails when want is not present in the ANSI-stripped view.
func assertPlainIncludes(t *testing.T, view, want string) {
	t.Helper()
	plain := stripANSI(view)
	if !strings.Contains(plain, want) {
		t.Fatalf("rendered view does not include %q (plain text below):\n%s", want, plain)
	}
}

// assertPlainExcludes fails when want is present in the ANSI-stripped view.
func assertPlainExcludes(t *testing.T, view, want string) {
	t.Helper()
	plain := stripANSI(view)
	if strings.Contains(plain, want) {
		t.Fatalf("rendered view includes forbidden %q (plain text below):\n%s", want, plain)
	}
}

// assertPlainAppearsBefore fails when first or second is missing or out of order.
func assertPlainAppearsBefore(t *testing.T, view, first, second string) {
	t.Helper()
	plain := stripANSI(view)
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

// assertPlainSameRow fails when first and second are not on the same line.
func assertPlainSameRow(t *testing.T, view, first, second string) {
	t.Helper()
	plain := stripANSI(view)
	for line := range strings.SplitSeq(plain, "\n") {
		if strings.Contains(line, first) && strings.Contains(line, second) {
			return
		}
	}
	t.Fatalf("%q and %q are not on the same line (plain text below):\n%s", first, second, plain)
}

// assertPlainNotSameRow fails when first and second appear on the same line.
func assertPlainNotSameRow(t *testing.T, view, first, second string) {
	t.Helper()
	plain := stripANSI(view)
	for line := range strings.SplitSeq(plain, "\n") {
		if strings.Contains(line, first) && strings.Contains(line, second) {
			t.Fatalf("%q and %q must not be on the same line (plain text below):\n%s", first, second, plain)
		}
	}
}

func TestStripANSI(t *testing.T) {
	const raw = "\x1b[31mhello\x1b[0m world"
	if got := stripANSI(raw); got != "hello world" {
		t.Fatalf("stripANSI() = %q, want %q", got, "hello world")
	}
}

func TestAssertPlainIncludes(t *testing.T) {
	assertPlainIncludes(t, "plain text", "plain")
	assertPlainIncludes(t, "\x1b[1mbold\x1b[0m value", "bold")
}

func TestAssertPlainExcludes(t *testing.T) {
	assertPlainExcludes(t, "plain text", "missing")
	assertPlainExcludes(t, "\x1b[1mbold\x1b[0m value", "absent")
}

func TestAssertPlainAppearsBefore(t *testing.T) {
	assertPlainAppearsBefore(t, "Gallery Statistics\nFile Processing", "Gallery Statistics", "File Processing")
	assertPlainAppearsBefore(t, "\x1b[1mGallery Statistics\x1b[0m\nFile Processing", "Gallery Statistics", "File Processing")
}
