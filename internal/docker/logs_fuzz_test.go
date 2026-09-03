package docker

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// dockerLogFrame builds one frame of Docker's multiplexed log format:
// a stream-type byte, three reserved bytes, a big-endian uint32 payload
// length, then the payload.
//
// It lives in the fuzz file rather than logs_test.go because
// ClusterFuzzLite builds this target with go-118-fuzz-build, which lifts
// only the single _test.go file holding the Fuzz function into a normal
// package build. A helper in a sibling _test.go file is undefined there,
// so the target has to be self-contained. Ordinary tests are unaffected:
// same package, so logs_test.go still sees it.
func dockerLogFrame(streamType byte, payload []byte) []byte {
	frame := make([]byte, 8+len(payload))
	frame[0] = streamType
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[8:], payload)
	return frame
}

// FuzzDecodeContainerLogStream feeds arbitrary bytes through
// DecodeContainerLogStream (the hand-rolled demuxer for Docker's 8-byte-header
// multiplexed log format) and verifies the properties that make manual
// length-prefixed frame parsing worth fuzzing: it never panics on arbitrary
// input, it terminates on bounded input, and it never emits more bytes than
// it was given — a header claiming an oversized or bogus length must never
// turn into an over-read or an unbounded allocation.
func FuzzDecodeContainerLogStream(f *testing.F) {
	// Seed: empty input.
	f.Add([]byte{})
	// Seed: a single valid stdout frame.
	f.Add(dockerLogFrame(byte(ContainerLogStdout), []byte("hello stdout")))
	// Seed: a single valid stderr frame.
	f.Add(dockerLogFrame(byte(ContainerLogStderr), []byte("hello stderr")))
	// Seed: a header claiming a size larger than the payload actually
	// present (truncated frame) — header says 25 bytes, only a few follow.
	truncated := dockerLogFrame(byte(ContainerLogStdout), []byte("this is the full payload"))
	f.Add(truncated[:12])
	// Seed: a zero-size frame.
	f.Add(dockerLogFrame(byte(ContainerLogStdout), nil))
	// Seed: back-to-back frames on both streams.
	backToBack := append(
		dockerLogFrame(byte(ContainerLogStdout), []byte("first")),
		dockerLogFrame(byte(ContainerLogStderr), []byte("second"))...,
	)
	f.Add(backToBack)
	// Seed: a garbage stream-type byte, otherwise a well-formed header.
	f.Add(dockerLogFrame(0x7f, []byte("garbage stream type")))
	// Seed: a header claiming a size larger than maxLogFrameSize, which must
	// be skipped rather than allocated.
	oversized := make([]byte, 8)
	oversized[0] = byte(ContainerLogStdout)
	oversized[4], oversized[5], oversized[6], oversized[7] = 0xff, 0xff, 0xff, 0xff
	f.Add(oversized)
	// Seed: raw TTY-shaped data that happens to start with a byte <= 2
	// followed by three zero bytes — the looksMultiplexed false-positive
	// shape (only 6 bytes total, short of a full 8-byte header).
	f.Add([]byte{1, 0, 0, 0, 'h', 'i'})
	// Seed: a stream truncated mid-header.
	f.Add([]byte{byte(ContainerLogStdout), 0, 0, 0, 0})

	f.Fuzz(func(t *testing.T, input []byte) {
		var emitted int
		emit := func(stream ContainerLogStream, payload []byte) error {
			if stream != ContainerLogStdout && stream != ContainerLogStderr {
				t.Fatalf("emit called with unexpected stream type %d", stream)
			}
			emitted += len(payload)
			return nil
		}

		// The property under test is "no panic, no over-read" — the error
		// return (truncated/corrupt input) is expected and not itself a
		// failure.
		_ = DecodeContainerLogStream(bytes.NewReader(input), emit)

		if emitted > len(input) {
			t.Fatalf("emitted %d bytes, exceeds input length %d", emitted, len(input))
		}
	})
}
