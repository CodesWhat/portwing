package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestDecodeContainerLogStreamEmitsShortRawDataBeforeEOF(t *testing.T) {
	t.Parallel()

	reader, writer := io.Pipe()
	chunks := make(chan []byte, 8)
	done := make(chan error, 1)
	writeDone := make(chan error, 1)
	go func() {
		done <- DecodeContainerLogStream(reader, func(_ ContainerLogStream, payload []byte) error {
			chunks <- append([]byte(nil), payload...)
			return nil
		})
	}()
	t.Cleanup(func() {
		_ = reader.Close()
		_ = writer.Close()
	})

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	go func() {
		_, err := writer.Write([]byte("tty\n"))
		writeDone <- err
	}()

	var got []byte
	for len(got) < len("tty\n") {
		select {
		case chunk := <-chunks:
			got = append(got, chunk...)
		case <-ctx.Done():
			t.Fatalf("short raw data was not emitted before EOF: got %q", got)
		}
	}
	if string(got) != "tty\n" {
		t.Fatalf("decoded raw data = %q, want %q", got, "tty\n")
	}
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("write raw log data: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("raw log writer remained blocked after data was emitted")
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("close raw log writer: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("DecodeContainerLogStream: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("DecodeContainerLogStream did not stop after EOF")
	}
}

func TestDecodeContainerLogStreamPropagatesRawReaderAndEmitterErrors(t *testing.T) {
	t.Parallel()

	readErr := errors.New("raw read failed")
	if err := DecodeContainerLogStream(errReader{err: readErr}, func(ContainerLogStream, []byte) error {
		t.Fatal("emit called for a failed raw read")
		return nil
	}); !errors.Is(err, readErr) {
		t.Fatalf("DecodeContainerLogStream error = %v, want %v", err, readErr)
	}

	emitErr := errors.New("raw emit failed")
	var calls int
	err := DecodeContainerLogStream(strings.NewReader("raw tty log\n"), func(stream ContainerLogStream, payload []byte) error {
		calls++
		if stream != ContainerLogStdout {
			t.Fatalf("stream = %d, want stdout", stream)
		}
		if got := string(payload); got != "raw tty log\n" {
			t.Fatalf("payload = %q, want raw TTY log", got)
		}
		return emitErr
	})
	if !errors.Is(err, emitErr) {
		t.Fatalf("DecodeContainerLogStream error = %v, want %v", err, emitErr)
	}
	if calls != 1 {
		t.Fatalf("emit calls = %d, want 1", calls)
	}
}

func TestDecodeContainerLogStreamPropagatesMultiplexedEmitterError(t *testing.T) {
	t.Parallel()

	emitErr := errors.New("multiplexed emit failed")
	input := append(
		dockerLogFrame(byte(ContainerLogStderr), []byte("first\n")),
		dockerLogFrame(byte(ContainerLogStdout), []byte("must not be emitted\n"))...,
	)
	var calls int
	err := DecodeContainerLogStream(bytes.NewReader(input), func(stream ContainerLogStream, payload []byte) error {
		calls++
		if stream != ContainerLogStderr {
			t.Fatalf("stream = %d, want stderr", stream)
		}
		if got := string(payload); got != "first\n" {
			t.Fatalf("payload = %q, want first frame", got)
		}
		return emitErr
	})
	if !errors.Is(err, emitErr) {
		t.Fatalf("DecodeContainerLogStream error = %v, want %v", err, emitErr)
	}
	if calls != 1 {
		t.Fatalf("emit calls = %d, want decoder to stop after first error", calls)
	}
}

func dockerLogHeader(streamType byte, size uint32) []byte {
	header := make([]byte, 8)
	header[0] = streamType
	binary.BigEndian.PutUint32(header[4:8], size)
	return header
}

func TestDecodeContainerLogs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      []byte
		want    []byte
		wantErr bool
	}{
		{
			name: "multiplexed stream is demuxed",
			in: append(
				dockerLogFrame(1, []byte("stdout\n")),
				dockerLogFrame(2, []byte("stderr\n"))...,
			),
			want: []byte("stdout\nstderr\n"),
		},
		{
			name: "raw tty text passes through unchanged",
			in:   []byte("hello world\nline two\n"),
			want: []byte("hello world\nline two\n"),
		},
		{
			name: "short input passes through unchanged",
			in:   []byte("short"),
			want: []byte("short"),
		},
		{
			name: "empty input returns empty output",
			in:   nil,
			want: []byte{},
		},
		{
			name: "raw stream with non header prefix passes through unchanged",
			in:   []byte{3, 0, 0, 0, 0, 0, 0, 5, 'h', 'e', 'l', 'l', 'o'},
			want: []byte{3, 0, 0, 0, 0, 0, 0, 5, 'h', 'e', 'l', 'l', 'o'},
		},
		{
			name: "truncated multiplexed frame returns partial bytes and error",
			in: append(
				dockerLogHeader(1, uint32(len("partial\n"))),
				[]byte("part")...,
			),
			want:    []byte("part"),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := DecodeContainerLogs(bytes.NewReader(tc.in))
			if tc.wantErr && err == nil {
				t.Fatal("DecodeContainerLogs returned nil error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("DecodeContainerLogs returned error: %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("DecodeContainerLogs = %q, want %q", string(got), string(tc.want))
			}
		})
	}
}

type errReader struct {
	err error
}

func (r errReader) Read([]byte) (int, error) {
	return 0, r.err
}
