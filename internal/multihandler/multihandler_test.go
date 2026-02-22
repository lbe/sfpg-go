package multihandler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// mockHandler wraps a TextHandler to write to a buffer and allows controlling its behavior for tests.
type mockHandler struct {
	slog.Handler
	buf     *bytes.Buffer
	enabled bool
	err     error
}

// newMockHandler creates a mock handler for testing.
func newMockHandler(buf *bytes.Buffer, level slog.Level, enabled bool, err error) *mockHandler {
	if buf == nil {
		buf = new(bytes.Buffer)
	}
	return &mockHandler{
		// We embed a real TextHandler to avoid reimplementing attribute and group handling.
		Handler: slog.NewTextHandler(buf, &slog.HandlerOptions{Level: level}),
		buf:     buf,
		enabled: enabled,
		err:     err,
	}
}

// Enabled overrides the embedded Handler's Enabled method for testing.
func (h *mockHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.enabled
}

// Handle overrides the embedded Handler's Handle method to allow injecting errors.
func (h *mockHandler) Handle(ctx context.Context, r slog.Record) error {
	if h.err != nil {
		return h.err
	}
	return h.Handler.Handle(ctx, r)
}

func TestNewMultiHandler(t *testing.T) {
	t.Run("no handlers", func(t *testing.T) {
		mh := NewMultiHandler()
		if mh == nil {
			t.Fatal("NewMultiHandler returned nil")
		}
		if len(mh.handlers) != 0 {
			t.Errorf("expected 0 handlers, got %d", len(mh.handlers))
		}
	})

	t.Run("with handlers", func(t *testing.T) {
		h1 := newMockHandler(nil, slog.LevelInfo, true, nil)
		h2 := newMockHandler(nil, slog.LevelInfo, true, nil)
		mh := NewMultiHandler(h1, h2)
		if len(mh.handlers) != 2 {
			t.Errorf("expected 2 handlers, got %d", len(mh.handlers))
		}
	})
}

func TestMultiHandler_Enabled(t *testing.T) {
	ctx := context.Background()
	level := slog.LevelInfo

	t.Run("no handlers", func(t *testing.T) {
		mh := NewMultiHandler()
		if mh.Enabled(ctx, level) {
			t.Error("expected Enabled to be false with no handlers")
		}
	})

	t.Run("one handler enabled", func(t *testing.T) {
		h1 := newMockHandler(nil, level, false, nil)
		h2 := newMockHandler(nil, level, true, nil)
		mh := NewMultiHandler(h1, h2)
		if !mh.Enabled(ctx, level) {
			t.Error("expected Enabled to be true when one handler is enabled")
		}
	})

	t.Run("no handlers enabled", func(t *testing.T) {
		h1 := newMockHandler(nil, level, false, nil)
		h2 := newMockHandler(nil, level, false, nil)
		mh := NewMultiHandler(h1, h2)
		if mh.Enabled(ctx, level) {
			t.Error("expected Enabled to be false when no handlers are enabled")
		}
	})
}

func TestMultiHandler_Handle(t *testing.T) {
	ctx := context.Background()
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "test message", 0)

	t.Run("dispatches to all handlers", func(t *testing.T) {
		buf1 := new(bytes.Buffer)
		buf2 := new(bytes.Buffer)
		h1 := newMockHandler(buf1, slog.LevelDebug, true, nil)
		h2 := newMockHandler(buf2, slog.LevelDebug, true, nil)
		mh := NewMultiHandler(h1, h2)

		if err := mh.Handle(ctx, record); err != nil {
			t.Fatalf("Handle failed: %v", err)
		}

		if !strings.Contains(buf1.String(), "test message") {
			t.Errorf("handler 1 did not receive the message, buffer: %q", buf1.String())
		}
		if !strings.Contains(buf2.String(), "test message") {
			t.Errorf("handler 2 did not receive the message, buffer: %q", buf2.String())
		}
	})

	t.Run("stops on error", func(t *testing.T) {
		testErr := errors.New("handler error")
		buf1 := new(bytes.Buffer)
		buf2 := new(bytes.Buffer)
		h1 := newMockHandler(buf1, slog.LevelDebug, true, testErr)
		h2 := newMockHandler(buf2, slog.LevelDebug, true, nil)
		mh := NewMultiHandler(h1, h2)

		err := mh.Handle(ctx, record)
		if !errors.Is(err, testErr) {
			t.Fatalf("expected error %v, got %v", testErr, err)
		}

		if buf2.Len() > 0 {
			t.Errorf("handler 2 should not have been called, but its buffer is: %q", buf2.String())
		}
	})
}

func TestMultiHandler_WithAttrs(t *testing.T) {
	buf1 := new(bytes.Buffer)
	buf2 := new(bytes.Buffer)
	h1 := newMockHandler(buf1, slog.LevelDebug, true, nil)
	h2 := newMockHandler(buf2, slog.LevelDebug, true, nil)
	mh := NewMultiHandler(h1, h2)

	attrs := []slog.Attr{slog.String("key", "value")}
	mhWithAttrs := mh.WithAttrs(attrs)

	logger := slog.New(mhWithAttrs)
	logger.Info("message with attrs")

	output1 := buf1.String()
	output2 := buf2.String()

	if !strings.Contains(output1, "key=value") {
		t.Errorf("handler 1 did not receive attributes: %q", output1)
	}
	if !strings.Contains(output2, "key=value") {
		t.Errorf("handler 2 did not receive attributes: %q", output2)
	}
}

func TestMultiHandler_WithGroup(t *testing.T) {
	buf1 := new(bytes.Buffer)
	buf2 := new(bytes.Buffer)
	h1 := newMockHandler(buf1, slog.LevelDebug, true, nil)
	h2 := newMockHandler(buf2, slog.LevelDebug, true, nil)
	mh := NewMultiHandler(h1, h2)

	mhWithGroup := mh.WithGroup("my_group")

	logger := slog.New(mhWithGroup)
	logger.Info("message with group", "key", "value")

	output1 := buf1.String()
	output2 := buf2.String()

	if !strings.Contains(output1, "my_group.key=value") {
		t.Errorf("handler 1 did not receive group: %q", output1)
	}
	if !strings.Contains(output2, "my_group.key=value") {
		t.Errorf("handler 2 did not receive group: %q", output2)
	}
}
