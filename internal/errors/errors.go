//
// Copyright (c) 2026 Learned By Error.  All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.
//

// Package errors provides a small, standard-library-compatible error wrapping
// package used internally by dque.
//
// The New, Wrap and Wrapf functions each capture a stack trace at the call site.
// The stack trace is rendered only with the %%+v format verb (via fmt.Formatter);
// Error() returns only the message (or message chain), matching the behavior
// of github.com/pkg/errors. Prefer errors.Is and errors.As for inspection over
// string comparison.
package errors

import (
	"errors"
	"fmt"
	"io"
	"runtime"
)

// New returns a new error with the given message.
// It captures a stack trace that is rendered with %%+v (via fmt.Formatter),
// matching the behavior of github.com/pkg/errors.
func New(msg string) error {
	return &fundamental{
		msg:   msg,
		stack: captureStack(),
	}
}

// fundamental is an error with a message and a stack trace but no cause.
// It provides Error() for the message and Format so that %%+v renders the stack.
type fundamental struct {
	msg   string
	stack []uintptr
}

// Error returns the error message (no stack trace).
func (f *fundamental) Error() string {
	return f.msg
}

// Format implements fmt.Formatter so that %%+v renders the stack trace
// while %%v, %%s, and %%q render only the message.
func (f *fundamental) Format(s fmt.State, verb rune) {
	switch verb {
	case 'v':
		if s.Flag('+') {
			fmt.Fprint(s, f.msg)
			renderStack(s, f.stack)
			return
		}
		fallthrough
	case 's':
		fmt.Fprint(s, f.msg)
	case 'q':
		fmt.Fprintf(s, "%q", f.msg)
	}
}

// Wrap returns a new error that wraps the given error with the given message.
// If err is nil, Wrap returns nil.
// The returned error captures a stack trace that is rendered with %+v.
func Wrap(err error, msg string) error {
	if err == nil {
		return nil
	}
	return &wrappedError{
		msg:   msg,
		cause: err,
		stack: captureStack(),
	}
}

// Wrapf returns a new error that wraps the given error with a formatted message.
// If err is nil, Wrapf returns nil.
// The returned error captures a stack trace that is rendered with %+v.
func Wrapf(err error, format string, args ...any) error {
	if err == nil {
		return nil
	}
	return &wrappedError{
		msg:   fmt.Sprintf(format, args...),
		cause: err,
		stack: captureStack(),
	}
}

// wrappedError is an error that wraps another error and captures a stack trace.
type wrappedError struct {
	msg   string
	cause error
	stack []uintptr
}

// Error returns the error message chain without a stack trace.
func (e *wrappedError) Error() string {
	return e.msg + ": " + e.cause.Error()
}

// Format implements fmt.Formatter so that %+v renders the stack trace
// while %v, %s, and %q render only the message chain (same as Error()).
func (e *wrappedError) Format(s fmt.State, verb rune) {
	switch verb {
	case 'v':
		if s.Flag('+') {
			// %+v: render the cause chain with stack traces
			fmt.Fprintf(s, "%+v\n", e.cause)
			fmt.Fprint(s, e.msg)
			renderStack(s, e.stack)
			return
		}
		fallthrough
	case 's':
		fmt.Fprint(s, e.Error())
	case 'q':
		fmt.Fprintf(s, "%q", e.Error())
	}
}

// Unwrap returns the wrapped error.
func (e *wrappedError) Unwrap() error {
	return e.cause
}

// captureStack captures the current call stack, skipping the captureStack
// function and its caller (New, Wrap, or Wrapf) and any runtime frames
// internal to this package.
func captureStack() []uintptr {
	const maxDepth = 32
	pcs := make([]uintptr, maxDepth)
	// Skip 3 frames: runtime.Callers, captureStack, and the Wrap/Wrapf caller.
	n := runtime.Callers(3, pcs)
	return pcs[:n]
}

// renderStack writes a human-readable rendering of the stack to w.
func renderStack(w io.Writer, pcs []uintptr) {
	frames := runtime.CallersFrames(pcs)
	for {
		frame, more := frames.Next()
		fmt.Fprint(w, "\n")
		fmt.Fprint(w, frame.Function)
		fmt.Fprint(w, "\n\t")
		fmt.Fprint(w, frame.File)
		fmt.Fprint(w, ":")
		fmt.Fprint(w, frame.Line)
		if !more {
			break
		}
	}
}

// Join returns an error that wraps the given errors. Any nil error values are
// discarded. Join returns nil if every value in errs is nil.
// The error formats as the concatenation of the strings obtained by calling
// the Error method of each element, with a newline between them.
func Join(errs ...error) error {
	return errors.Join(errs...)
}

// Is delegates to the standard library errors.Is.
func Is(err error, target error) bool {
	return errors.Is(err, target)
}
