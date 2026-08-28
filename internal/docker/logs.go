package docker

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// maxLogFrameSize bounds a single multiplexed log frame's payload. A frame
// header claiming more than this is skipped rather than allocated, so a corrupt
// or hostile header can't drive an oversized buffer allocation.
const maxLogFrameSize = 256 << 10 // 256 KiB

// ContainerLogStream identifies the source stream for a decoded Docker log
// payload. Raw TTY logs are emitted as stdout because Docker does not retain a
// separate stream identifier for them.
type ContainerLogStream byte

const (
	ContainerLogStdout ContainerLogStream = 1
	ContainerLogStderr ContainerLogStream = 2
)

// DecodeContainerLogStream decodes either raw TTY logs or Docker's
// multiplexed log format and calls emit for each payload in stream order.
func DecodeContainerLogStream(r io.Reader, emit func(ContainerLogStream, []byte) error) error {
	br := bufio.NewReaderSize(r, 8)
	prefix, _ := br.Peek(1)
	if len(prefix) == 1 && prefix[0] <= 2 {
		prefix, _ = br.Peek(4)
		if len(prefix) == 4 && prefix[1] == 0 && prefix[2] == 0 && prefix[3] == 0 {
			header, _ := br.Peek(8)
			if looksMultiplexed(header) {
				return decodeMultiplexedLogStream(br, emit)
			}
		}
	}

	buffer := make([]byte, 32<<10)
	for {
		n, err := br.Read(buffer)
		if n > 0 {
			if emitErr := emit(ContainerLogStdout, buffer[:n]); emitErr != nil {
				return emitErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// DecodeContainerLogs returns the plain log text from a Docker container-log
// response body, transparently handling both stream shapes the daemon can emit:
// a non-TTY container's stream is multiplexed with 8-byte frame headers (see
// DemuxLogStream), while a TTY container's stream is raw text with no headers.
// Demuxing a raw stream would corrupt it (the first bytes get misread as a frame
// header), so the two are told apart by peeking the leading bytes: a multiplexed
// stream always begins with a valid frame header ([stream_type in 0..2, 0, 0, 0,
// size]), which a raw text stream effectively never does. This avoids both a
// container inspect round-trip and reliance on a daemon Content-Type header that
// pre-1.42 daemons don't set. Wrap r in an io.LimitReader to bound total size.
func DecodeContainerLogs(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	err := DecodeContainerLogStream(r, func(_ ContainerLogStream, payload []byte) error {
		_, writeErr := buf.Write(payload)
		return writeErr
	})
	return buf.Bytes(), err
}

// looksMultiplexed reports whether b begins with a Docker multiplexed-stream
// frame header: byte 0 is the stream type (0=stdin, 1=stdout, 2=stderr) and
// bytes 1-3 are always zero. Raw TTY output does not start with this pattern.
func looksMultiplexed(b []byte) bool {
	return len(b) >= 8 && b[0] <= 2 && b[1] == 0 && b[2] == 0 && b[3] == 0
}

// DemuxLogStream reads Docker's multiplexed container-log stream from r and
// returns the concatenated payload with the 8-byte per-frame headers stripped.
// Docker prefixes each frame of a non-TTY container's log stream with an 8-byte
// header [stream_type(1), 0, 0, 0, size(4 big-endian)]; this returns just the
// payload text, matching what the streaming HTTP /logs route emits for the same
// container (internal/adapter/drydock/routes.go). It is the buffered,
// read-into-memory counterpart to that streaming demux, for callers that ship
// logs in a single message rather than a chunked response.
//
// Bytes read before a read error are still returned, so a caller that bounds the
// stream (e.g. io.LimitReader, or a follow window that ends the read early) gets
// the accumulated logs alongside the error and can choose to use them. Frames
// larger than maxLogFrameSize are skipped. The caller should bound the total
// read for overall size safety; DemuxLogStream only bounds per-frame allocation.
func DemuxLogStream(r io.Reader) ([]byte, error) {
	var buf bytes.Buffer
	err := decodeMultiplexedLogStream(r, func(_ ContainerLogStream, payload []byte) error {
		_, writeErr := buf.Write(payload)
		return writeErr
	})
	return buf.Bytes(), err
}

func decodeMultiplexedLogStream(r io.Reader, emit func(ContainerLogStream, []byte) error) error {
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}

		size := binary.BigEndian.Uint32(hdr[4:8])
		if size == 0 {
			continue
		}

		if size > maxLogFrameSize {
			if _, err := io.CopyN(io.Discard, r, int64(size)); err != nil {
				return fmt.Errorf("skipping oversized log frame (%d bytes): %w", size, err)
			}
			continue
		}

		payload := make([]byte, size)
		n, err := io.ReadFull(r, payload)
		if n > 0 {
			stream := ContainerLogStdout
			if hdr[0] == byte(ContainerLogStderr) {
				stream = ContainerLogStderr
			}
			if emitErr := emit(stream, payload[:n]); emitErr != nil {
				return emitErr
			}
		}
		if err != nil {
			return err
		}
	}
}
