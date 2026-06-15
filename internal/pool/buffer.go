package pool

import "sync"

const BufferSize = 4096

var Pool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, BufferSize)
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
