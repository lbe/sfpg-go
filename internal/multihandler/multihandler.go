// Package multihandler provides a slog.Handler implementation that dispatches
// log records to multiple underlying slog.Handler instances. This allows
// a single log event to be processed by different handlers (e.g., console, file).
package multihandler

import (
	"context"
	"log/slog"
)

// MultiHandler is an implementation of slog.Handler that wraps multiple
// other slog.Handler instances. When a log record is processed by a MultiHandler,
// it dispatches the record to all its contained handlers.
type MultiHandler struct {
	handlers []slog.Handler
}

// NewMultiHandler creates and returns a new MultiHandler that will dispatch
// log records to the provided handlers.
func NewMultiHandler(handlers ...slog.Handler) *MultiHandler {
	return &MultiHandler{handlers: handlers}
}

// Enabled reports whether a handler would log at the given level. It returns
// true if any of the wrapped handlers are enabled for the given level.
func (h *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle dispatches the log Record to all wrapped handlers. If any handler
// returns an error, processing stops and that error is returned.
func (h *MultiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, handler := range h.handlers {
		if err := handler.Handle(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

// WithAttrs returns a new MultiHandler whose handlers are all derived
// from the original handlers, each augmented with the provided attributes.
func (h *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	var handlers []slog.Handler
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithAttrs(attrs))
	}
	return NewMultiHandler(handlers...)
}

// WithGroup returns a new MultiHandler whose handlers are all derived
// from the original handlers, each with a new log group appended.
func (h *MultiHandler) WithGroup(name string) slog.Handler {
	var handlers []slog.Handler
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithGroup(name))
	}
	return NewMultiHandler(handlers...)
}
