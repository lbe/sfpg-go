package tuiview

import "testing"

func TestStripANSI(t *testing.T) {
	const raw = "\x1b[31mhello\x1b[0m world"
	if got := StripANSI(raw); got != "hello world" {
		t.Fatalf("StripANSI() = %q, want %q", got, "hello world")
	}
}

func TestAssertPlainIncludes(t *testing.T) {
	AssertPlainIncludes(t, "plain text", "plain")
	AssertPlainIncludes(t, "\x1b[1mbold\x1b[0m value", "bold")
}

func TestAssertPlainExcludes(t *testing.T) {
	AssertPlainExcludes(t, "plain text", "missing")
	AssertPlainExcludes(t, "\x1b[1mbold\x1b[0m value", "absent")
}

func TestAssertPlainAppearsBefore(t *testing.T) {
	AssertPlainAppearsBefore(t, "Gallery Statistics\nFile Processing", "Gallery Statistics", "File Processing")
	AssertPlainAppearsBefore(t, "\x1b[1mGallery Statistics\x1b[0m\nFile Processing", "Gallery Statistics", "File Processing")
}
