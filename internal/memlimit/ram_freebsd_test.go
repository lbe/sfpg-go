package memlimit

import "testing"

func TestTotalRAM_FreeBSD(t *testing.T) {
	if got := totalRAM(); got == 0 {
		t.Fatalf("totalRAM() = %d, want > 0", got)
	}
}
