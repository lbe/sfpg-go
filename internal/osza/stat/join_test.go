package stat

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorsIsErrNilFileMeta(t *testing.T) {
	t.Parallel()
	wrapped := fmt.Errorf("wrap: %w", ErrNilFileMeta)
	if !errors.Is(wrapped, ErrNilFileMeta) {
		t.Fatal("expected errors.Is(wrapped, ErrNilFileMeta)")
	}
	if errors.Is(wrapped, ErrPathBuffer) {
		t.Fatal("ErrNilFileMeta must not match ErrPathBuffer")
	}
	if !errors.Is(ErrNilFileMeta, ErrNilFileMeta) {
		t.Fatal("expected sentinel identity")
	}
}

func TestJoinPacked(t *testing.T) {
	t.Parallel()

	t.Run("parentAndName", func(t *testing.T) {
		t.Parallel()
		const parentLen = 3
		const nameLen = 3
		buf := make([]byte, parentLen+1+nameLen+1)
		copy(buf[:parentLen], "foo")
		copy(buf[parentLen:parentLen+nameLen], "bar")

		pathLen, err := joinPacked(buf, parentLen, nameLen)
		if err != nil {
			t.Fatalf("joinPacked: %v", err)
		}
		wantLen := parentLen + 1 + nameLen
		if pathLen != wantLen {
			t.Fatalf("pathLen = %d, want %d", pathLen, wantLen)
		}
		want := "foo/bar"
		if string(buf[:pathLen]) != want {
			t.Fatalf("path = %q, want %q", buf[:pathLen], want)
		}
		if buf[pathLen] != 0 {
			t.Fatalf("expected NUL at buf[pathLen], got %q", buf[pathLen])
		}
	})

	t.Run("emptyName", func(t *testing.T) {
		t.Parallel()
		buf := make([]byte, 8)
		copy(buf[:4], "root")

		pathLen, err := joinPacked(buf, 4, 0)
		if err != nil {
			t.Fatalf("joinPacked: %v", err)
		}
		if pathLen != 5 {
			t.Fatalf("pathLen = %d, want 5", pathLen)
		}
		if string(buf[:pathLen]) != "root/" {
			t.Fatalf("path = %q", buf[:pathLen])
		}
	})

	t.Run("errPathBufferTooSmallForNUL", func(t *testing.T) {
		t.Parallel()
		// Packed layout fits in 6 bytes but not room for separator + NUL.
		buf := make([]byte, 6)
		copy(buf[:3], "foo")
		copy(buf[3:6], "bar")

		_, err := joinPacked(buf, 3, 3)
		if !errors.Is(err, ErrPathBuffer) {
			t.Fatalf("err = %v, want ErrPathBuffer", err)
		}
	})

	t.Run("errPathBufferNamePastLen", func(t *testing.T) {
		t.Parallel()
		buf := make([]byte, 4)
		copy(buf[:2], "ab")
		copy(buf[2:4], "cd")

		_, err := joinPacked(buf, 2, 3)
		if !errors.Is(err, ErrPathBuffer) {
			t.Fatalf("err = %v, want ErrPathBuffer", err)
		}
	})

	t.Run("errPathBufferNegativeLens", func(t *testing.T) {
		t.Parallel()
		buf := make([]byte, 8)
		_, err := joinPacked(buf, -1, 1)
		if !errors.Is(err, ErrPathBuffer) {
			t.Fatalf("err = %v, want ErrPathBuffer", err)
		}
	})
}
