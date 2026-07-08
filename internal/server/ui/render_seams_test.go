package ui

import "errors"

// errorWriter is an io.Writer that always fails.
type errorWriter struct{}

func (errorWriter) Write(p []byte) (int, error) {
	return 0, errors.New("write failed")
}
