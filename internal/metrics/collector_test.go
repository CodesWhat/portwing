package metrics

import (
	"math"
	"testing"
)

func TestStatfsBytesRejectsInvalidBlockSize(t *testing.T) {
	t.Parallel()

	if got := statfsBytes(10, -1); got != 0 {
		t.Fatalf("statfsBytes with negative block size = %d, want 0", got)
	}
	if got := statfsBytes(10, 0); got != 0 {
		t.Fatalf("statfsBytes with zero block size = %d, want 0", got)
	}
}

func TestStatfsBytesSaturatesOverflow(t *testing.T) {
	t.Parallel()

	if got := statfsBytes(math.MaxUint64, 2); got != math.MaxUint64 {
		t.Fatalf("statfsBytes overflow = %d, want %d", got, uint64(math.MaxUint64))
	}
}
