// SPDX-License-Identifier: MIT

package docker

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"mime"
	"strconv"
	"strings"
)

// maxLogFrameSize bounds the allocation used to decode one multiplexed frame.
// Larger frames are streamed in chunks within this limit.
const maxLogFrameSize = 256 << 10 // 256 KiB

type logStreamFormat byte

const (
	logFormatAuto logStreamFormat = iota
	logFormatRaw
	logFormatMultiplexed
)

type logFormatSource interface {
	containerLogFormat() logStreamFormat
}

type formattedLogReader struct {
	io.Reader
	format logStreamFormat
}

func (r formattedLogReader) containerLogFormat() logStreamFormat { return r.format }

type formattedLogReadCloser struct {
	io.ReadCloser
	format logStreamFormat
}

func (r formattedLogReadCloser) containerLogFormat() logStreamFormat { return r.format }

func responseLogFormat(contentType, apiVersion string) logStreamFormat {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return logFormatAuto
	}
	if mediaType == "application/vnd.docker.multiplexed-stream" {
		return logFormatMultiplexed
	}
	if mediaType == "application/vnd.docker.raw-stream" {
		// Before API 1.42 Docker used raw-stream for both log formats.
		majorText, minorText, ok := strings.Cut(strings.TrimPrefix(apiVersion, "v"), ".")
		major, majorErr := strconv.Atoi(majorText)
		minor, minorErr := strconv.Atoi(minorText)
		if ok && majorErr == nil && minorErr == nil && (major > 1 || (major == 1 && minor >= 42)) {
			return logFormatRaw
		}
	}
	return logFormatAuto
}

// LimitContainerLogStream bounds reads while preserving the daemon's log format.
func LimitContainerLogStream(r io.Reader, limit int64) io.Reader {
	limited := io.LimitReader(r, limit)
	if source, ok := r.(logFormatSource); ok {
		return formattedLogReader{Reader: limited, format: source.containerLogFormat()}
	}
	return limited
}

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
	format := logFormatAuto
	if source, ok := r.(logFormatSource); ok {
		format = source.containerLogFormat()
	}
	if format == logFormatMultiplexed {
		return decodeMultiplexedLogStream(r, emit)
	}
	br := bufio.NewReaderSize(r, 8)
	prefix, _ := br.Peek(1)
	if format != logFormatRaw && len(prefix) == 1 && prefix[0] <= 2 {
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
// a non-TTY container's stream is multiplexed with 8-byte frame headers, while
// a TTY container's stream is raw text with no headers.
// Demuxing a raw stream would corrupt it (the first bytes get misread as a frame
// header). GetContainerLogs carries the daemon's format through to the decoder;
// callers without that metadata retain the legacy leading-byte heuristic.
// Use LimitContainerLogStream to bound total size without losing format metadata.
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
// bytes 1-3 are always zero. Raw TTY output can share this prefix, so the
// heuristic is used only when the daemon's format is unavailable.
func looksMultiplexed(b []byte) bool {
	return len(b) >= 8 && b[0] <= 2 && b[1] == 0 && b[2] == 0 && b[3] == 0
}

func decodeMultiplexedLogStream(r io.Reader, emit func(ContainerLogStream, []byte) error) error {
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		size := binary.BigEndian.Uint32(hdr[4:8])
		if size == 0 {
			continue
		}

		stream := ContainerLogStdout
		if hdr[0] == byte(ContainerLogStderr) {
			stream = ContainerLogStderr
		}
		chunkSize := min(size, maxLogFrameSize)
		buffer := make([]byte, chunkSize)
		for remaining := size; remaining > 0; {
			partSize := min(remaining, chunkSize)
			payload := buffer[:partSize]
			n, err := io.ReadFull(r, payload)
			if n > 0 {
				if emitErr := emit(stream, payload[:n]); emitErr != nil {
					return emitErr
				}
			}
			if err != nil {
				return err
			}
			remaining -= partSize
		}
	}
}
