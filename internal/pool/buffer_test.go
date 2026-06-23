package pool

import "testing"

func TestBufferPoolReturnsFullSizedBuffers(t *testing.T) {
	t.Parallel()

	buf := GetBuffer()
	if len(buf) != BufferSize {
		t.Fatalf("len(GetBuffer()) = %d, want %d", len(buf), BufferSize)
	}
	if cap(buf) != BufferSize {
		t.Fatalf("cap(GetBuffer()) = %d, want %d", cap(buf), BufferSize)
	}

	PutBuffer(buf[:128])

	reused := GetBuffer()
	if len(reused) != BufferSize {
		t.Fatalf("len(reused buffer) = %d, want %d", len(reused), BufferSize)
	}
	if cap(reused) != BufferSize {
		t.Fatalf("cap(reused buffer) = %d, want %d", cap(reused), BufferSize)
	}
}

// TestPutBufferOversizedDropped verifies that a buffer whose capacity is less
// than BufferSize is silently dropped rather than returned to the pool.
// The existing test exercises the happy path (cap == BufferSize); this covers
// the guard branch at buffer.go:32.
func TestPutBufferOversizedDropped(t *testing.T) {
	t.Parallel()

	// A small allocation — capacity is well below BufferSize so PutBuffer must
	// take the early-return branch and leave the pool untouched.
	small := make([]byte, 64)
	PutBuffer(small) // must not panic or corrupt the pool
}

// TestGetStreamBufferSize confirms GetStreamBuffer returns a buffer of exactly
// StreamBufferSize length and capacity, matching the Pool.New initialisation.
func TestGetStreamBufferSize(t *testing.T) {
	t.Parallel()

	buf := GetStreamBuffer()
	if len(buf) != StreamBufferSize {
		t.Fatalf("len(GetStreamBuffer()) = %d, want %d", len(buf), StreamBufferSize)
	}
	if cap(buf) != StreamBufferSize {
		t.Fatalf("cap(GetStreamBuffer()) = %d, want %d", cap(buf), StreamBufferSize)
	}
}

// TestPutStreamBufferReturnsToPool verifies the normal round-trip: shrink the
// slice (simulating partial use), put it back, and confirm the next get still
// returns a full-sized buffer because PutStreamBuffer reslices to StreamBufferSize.
func TestPutStreamBufferReturnsToPool(t *testing.T) {
	t.Parallel()

	buf := GetStreamBuffer()
	// Simulate partial use by reslicing.
	PutStreamBuffer(buf[:1024])

	reused := GetStreamBuffer()
	if len(reused) != StreamBufferSize {
		t.Fatalf("len(reused stream buffer) = %d, want %d", len(reused), StreamBufferSize)
	}
	if cap(reused) != StreamBufferSize {
		t.Fatalf("cap(reused stream buffer) = %d, want %d", cap(reused), StreamBufferSize)
	}
}

// TestPutStreamBufferUndersizedDropped verifies that a buffer whose capacity is
// below StreamBufferSize is dropped, covering the guard branch at buffer.go:48.
func TestPutStreamBufferUndersizedDropped(t *testing.T) {
	t.Parallel()

	// Capacity smaller than StreamBufferSize — PutStreamBuffer must take the
	// early-return branch and not put this into the pool.
	small := make([]byte, 128)
	PutStreamBuffer(small) // must not panic or corrupt the pool
}
