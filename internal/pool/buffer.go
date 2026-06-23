package pool

import "sync"

const BufferSize = 4096

// StreamBufferSize is the buffer size for proxied response streaming. It is
// larger than BufferSize so a large response (image pulls, log tails) moves in
// fewer WebSocket frames; these buffers come from StreamPool so the per-stream
// allocation is reused instead of freshly allocated per request.
const StreamBufferSize = 32 * 1024

var Pool = sync.Pool{
	New: func() any {
		buf := make([]byte, BufferSize)
		return &buf
	},
}

var StreamPool = sync.Pool{
	New: func() any {
		buf := make([]byte, StreamBufferSize)
		return &buf
	},
}

func GetBuffer() []byte {
	return *Pool.Get().(*[]byte)
}

func PutBuffer(buf []byte) {
	if cap(buf) < BufferSize {
		return
	}
	buf = buf[:BufferSize]
	Pool.Put(&buf)
}

// GetStreamBuffer returns a pooled StreamBufferSize buffer for the response
// streaming path. Return it with PutStreamBuffer.
func GetStreamBuffer() []byte {
	return *StreamPool.Get().(*[]byte)
}

// PutStreamBuffer returns a stream buffer to the pool. Buffers smaller than
// StreamBufferSize are dropped rather than pooled at the wrong size.
func PutStreamBuffer(buf []byte) {
	if cap(buf) < StreamBufferSize {
		return
	}
	buf = buf[:StreamBufferSize]
	StreamPool.Put(&buf)
}
