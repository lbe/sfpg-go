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
