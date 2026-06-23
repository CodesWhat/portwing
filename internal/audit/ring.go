package audit

import (
	"sync"
	"time"
)

// Record holds a single structured audit event captured in the ring buffer.
// Field names match the stable on-wire JSON schema documented in the package comment.
type Record struct {
	Time       time.Time `json:"ts"`
	Event      string    `json:"event"`
	Actor      string    `json:"actor,omitempty"`
	Method     string    `json:"method,omitempty"`
	Path       string    `json:"path,omitempty"`
	Outcome    string    `json:"outcome,omitempty"`
	Status     int       `json:"status,omitempty"`
	DurationMs float64   `json:"duration_ms,omitempty"`
	Operation  string    `json:"operation,omitempty"`
	Stack      string    `json:"stack,omitempty"`
	Container  string    `json:"container,omitempty"`
	ExecID     string    `json:"exec_id,omitempty"`
	KeyID      string    `json:"key_id,omitempty"`
}

// ring is a fixed-capacity circular buffer of Records. It is safe for
// concurrent use via its embedded mutex.
type ring struct {
	mu       sync.Mutex
	buf      []Record
	head     int // index of the next write slot
	size     int // number of valid entries currently stored
	capacity int
}

// newRing creates a ring buffer with the given capacity.
func newRing(capacity int) *ring {
	return &ring{
		buf:      make([]Record, capacity),
		capacity: capacity,
	}
}

// push appends r to the buffer, overwriting the oldest entry when full.
func (rb *ring) push(r Record) {
	rb.mu.Lock()
	rb.buf[rb.head] = r
	rb.head = (rb.head + 1) % rb.capacity
	if rb.size < rb.capacity {
		rb.size++
	}
	rb.mu.Unlock()
}

// records returns a copy of the buffered records, newest-first, capped at
// min(limit, size). A limit <= 0 means "return all currently buffered".
func (rb *ring) records(limit int) []Record {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	n := rb.size
	if limit > 0 && limit < n {
		n = limit
	}
	if n == 0 {
		return []Record{}
	}

	out := make([]Record, n)
	// The newest entry is at (head-1+capacity)%capacity; walk backwards.
	for i := 0; i < n; i++ {
		idx := (rb.head - 1 - i + rb.capacity) % rb.capacity
		out[i] = rb.buf[idx]
	}
	return out
}
