package errors

import (
	stderrors "errors"
	"fmt"
	"strings"
	"testing"
)

func TestNewReturnsPlainError(t *testing.T) {
	err := New("plain error")
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if err.Error() != "plain error" {
		t.Fatalf("expected error message %q, got %q", "plain error", err.Error())
	}
}

func TestWrapNilReturnsNil(t *testing.T) {
	if Wrap(nil, "msg") != nil {
		t.Fatal("Wrap(nil, ...) should return nil")
	}
	if Wrapf(nil, "msg %d", 1) != nil {
		t.Fatal("Wrapf(nil, ...) should return nil")
	}
}

func TestWrapFormatsMessage(t *testing.T) {
	cause := New("cause")
	err := Wrap(cause, "msg")
	if !strings.HasPrefix(err.Error(), "msg: cause") {
		t.Fatalf("expected error to start with %q, got %q", "msg: cause", err.Error())
	}
}

func TestWrapfFormatsMessage(t *testing.T) {
	cause := New("cause")
	err := Wrapf(cause, "msg %d", 42)
	if !strings.HasPrefix(err.Error(), "msg 42: cause") {
		t.Fatalf("expected error to start with %q, got %q", "msg 42: cause", err.Error())
	}
}

func TestWrapUnwrap(t *testing.T) {
	cause := New("cause")
	wrapped := Wrap(cause, "msg")

	if !stderrors.Is(wrapped, cause) {
		t.Fatal("expected errors.Is(wrapped, cause) to be true")
	}

	if _, ok := stderrors.AsType[*wrappedError](wrapped); !ok {
		t.Fatal("expected errors.As to find wrappedError")
	}
}

func TestWrapfUnwrap(t *testing.T) {
	cause := New("cause")
	wrapped := Wrapf(cause, "msg %d", 42)

	if !stderrors.Is(wrapped, cause) {
		t.Fatal("expected errors.Is(wrapped, cause) to be true")
	}
}

func TestNewHasNoStackTraceInError(t *testing.T) {
	err := New("plain error")
	if strings.Contains(err.Error(), "runtime.") {
		t.Fatal("Error() should not contain a stack trace")
	}
}

func TestNewIncludesStackTraceInFormatVPlus(t *testing.T) {
	err := New("plain error")
	rendered := fmt.Sprintf("%+v", err)
	if !strings.Contains(rendered, "runtime.") {
		t.Fatalf("expected stack trace in %%+v, got %q", rendered)
	}
	if !strings.Contains(rendered, "internal/errors.TestNewIncludesStackTraceInFormatVPlus") {
		t.Fatalf("expected %%+v to contain this test frame, got %q", rendered)
	}
}

func TestNewFormatV(t *testing.T) {
	err := New("plain error")
	got := fmt.Sprintf("%v", err)
	if got != "plain error" {
		t.Fatalf("%%v: expected %q, got %q", "plain error", got)
	}
}

func TestNewFormatS(t *testing.T) {
	err := New("plain error")
	got := fmt.Sprintf("%s", err)
	if got != "plain error" {
		t.Fatalf("%%s: expected %q, got %q", "plain error", got)
	}
}

func TestNewFormatQ(t *testing.T) {
	err := New("plain error")
	got := fmt.Sprintf("%q", err)
	if got != `"plain error"` {
		t.Fatalf("%%q: expected %q, got %q", `"plain error"`, got)
	}
}

func TestWrapIncludesStackTrace(t *testing.T) {
	cause := New("cause")
	err := Wrap(cause, "msg")
	rendered := fmt.Sprintf("%+v", err)
	if !strings.Contains(rendered, "internal/errors.TestWrapIncludesStackTrace") {
		t.Fatalf("expected stack trace in %%+v, got %q", rendered)
	}
}

func TestWrapRendersFullStackTrace(t *testing.T) {
	cause := New("cause")
	err := Wrap(cause, "msg")
	rendered := fmt.Sprintf("%+v", err)

	// The captured stack should contain this test frame at a minimum.
	if !strings.Contains(rendered, "internal/errors.TestWrapRendersFullStackTrace") {
		t.Fatalf("expected stack trace to contain this test frame, got %q", rendered)
	}

	// Count the number of frames rendered. Each frame contributes two newlines:
	// one after the function name and one after the file:line entry, so each frame
	// produces "\n<function>\n\t<file>:<line>". The rendered string starts with the
	// message, so the number of "\n\t" occurrences is the number of frames.
	frameCount := strings.Count(rendered, "\n\t")
	if frameCount == 0 {
		t.Fatal("expected at least one rendered stack frame")
	}

	// A regression check for the dropped-last-frame bug: ensure the final rendered
	// line is a file:line entry and not an empty or partial frame.
	trimmed := strings.TrimSpace(rendered)
	if !strings.Contains(trimmed, ".go:") {
		t.Fatalf("expected rendered stack to end with a file:line entry, got %q", trimmed)
	}
}

func TestWrapNoStackTraceInError(t *testing.T) {
	cause := New("cause")
	err := Wrap(cause, "msg")
	if strings.Contains(err.Error(), "\n") {
		t.Fatalf("expected no stack trace in Error(), got %q", err.Error())
	}
	if err.Error() != "msg: cause" {
		t.Fatalf("expected clean error message, got %q", err.Error())
	}
}

func TestWrapfNoStackTraceInError(t *testing.T) {
	cause := New("cause")
	err := Wrapf(cause, "msg %d", 42)
	if strings.Contains(err.Error(), "\n") {
		t.Fatalf("expected no stack trace in Error(), got %q", err.Error())
	}
	if err.Error() != "msg 42: cause" {
		t.Fatalf("expected clean error message, got %q", err.Error())
	}
}

func TestWrapFormatV(t *testing.T) {
	cause := New("cause")
	err := Wrap(cause, "msg")
	got := fmt.Sprintf("%v", err)
	if got != "msg: cause" {
		t.Fatalf("%%v: expected %q, got %q", "msg: cause", got)
	}
}

func TestWrapFormatS(t *testing.T) {
	cause := New("cause")
	err := Wrap(cause, "msg")
	got := fmt.Sprintf("%s", err)
	if got != "msg: cause" {
		t.Fatalf("%%s: expected %q, got %q", "msg: cause", got)
	}
}

func TestWrapFormatQ(t *testing.T) {
	cause := New("cause")
	err := Wrap(cause, "msg")
	got := fmt.Sprintf("%q", err)
	expected := `"msg: cause"`
	if got != expected {
		t.Fatalf("%%q: expected %q, got %q", expected, got)
	}
}

func TestJoin(t *testing.T) {
	err := Join(New("a"), New("b"))
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	if !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
		t.Fatalf("expected both errors in message, got %q", err.Error())
	}
}

func TestJoinAllNil(t *testing.T) {
	err := Join(nil, nil)
	if err != nil {
		t.Fatal("expected nil when all inputs are nil")
	}
}

func TestAs(t *testing.T) {
	cause := New("cause")
	wrapped := Wrap(cause, "msg")
	var target *wrappedError
	if !stderrors.As(wrapped, &target) {
		t.Fatal("expected stdlib errors.As to find wrappedError")
	}
	if target == nil {
		t.Fatal("expected non-nil target")
	}
}

func TestIs(t *testing.T) {
	cause := New("cause")
	wrapped := Wrap(cause, "msg")
	if !Is(wrapped, cause) {
		t.Fatal("expected Is(wrapped, cause) to be true")
	}
}

func TestIsNotFound(t *testing.T) {
	a := New("a")
	b := New("b")
	if Is(a, b) {
		t.Fatal("expected Is(a, b) to be false")
	}
}
