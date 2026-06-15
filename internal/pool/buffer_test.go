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
