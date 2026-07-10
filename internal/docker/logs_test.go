package docker

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

func dockerLogFrame(streamType byte, payload []byte) []byte {
	frame := make([]byte, 8+len(payload))
	frame[0] = streamType
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(payload)))
	copy(frame[8:], payload)
	return frame
}

func dockerLogHeader(streamType byte, size uint32) []byte {
	header := make([]byte, 8)
	header[0] = streamType
	binary.BigEndian.PutUint32(header[4:8], size)
	return header
}

func TestDemuxLogStream(t *testing.T) {
	t.Parallel()

	oversized := bytes.Repeat([]byte("x"), maxLogFrameSize+1)

	tests := []struct {
		name string
		in   []byte
		want string
	}{
		{
			name: "single frame",
			in:   dockerLogFrame(1, []byte("hello\n")),
			want: "hello\n",
		},
		{
			name: "multiple frames concatenated in order",
			in: append(
				dockerLogFrame(1, []byte("first\n")),
				dockerLogFrame(1, []byte("second\n"))...,
			),
			want: "first\nsecond\n",
		},
		{
			name: "zero-size frame skipped",
			in: append(
				dockerLogFrame(1, nil),
				dockerLogFrame(1, []byte("after\n"))...,
			),
			want: "after\n",
		},
		{
			name: "stdout and stderr payloads both included without prefixes",
			in: append(
				dockerLogFrame(1, []byte("stdout\n")),
				dockerLogFrame(2, []byte("stderr\n"))...,
			),
			want: "stdout\nstderr\n",
		},
		{
			name: "oversized frame skipped before following frame",
			in: append(
				append(dockerLogHeader(1, uint32(len(oversized))), oversized...),
				dockerLogFrame(1, []byte("kept\n"))...,
			),
			want: "kept\n",
		},
		{
			name: "empty reader",
			in:   nil,
			want: "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := DemuxLogStream(bytes.NewReader(tc.in))
			if err != nil {
				t.Fatalf("DemuxLogStream returned error: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("DemuxLogStream = %q, want %q", string(got), tc.want)
			}
		})
	}
}

func TestDemuxLogStream_TruncatedHeaderEOFIsCleanEnd(t *testing.T) {
	t.Parallel()

	input := append(dockerLogFrame(1, []byte("complete\n")), []byte{1, 0, 0, 0}...)

	got, err := DemuxLogStream(bytes.NewReader(input))
	if err != nil {
		t.Fatalf("DemuxLogStream returned error for short EOF header: %v", err)
	}
	if string(got) != "complete\n" {
		t.Fatalf("DemuxLogStream = %q, want %q", string(got), "complete\n")
	}
}

func TestDemuxLogStream_TruncatedHeaderReadErrorReturnsAccumulated(t *testing.T) {
	t.Parallel()

	readErr := errors.New("header read failed")
	reader := io.MultiReader(
		bytes.NewReader(dockerLogFrame(1, []byte("complete\n"))),
		&bytesThenErrReader{data: []byte{1, 0, 0, 0}, err: readErr},
	)

	got, err := DemuxLogStream(reader)
	if !errors.Is(err, readErr) {
		t.Fatalf("DemuxLogStream error = %v, want %v", err, readErr)
	}
	if string(got) != "complete\n" {
		t.Fatalf("DemuxLogStream = %q, want %q", string(got), "complete\n")
	}
}

func TestDemuxLogStream_TruncatedPayloadReturnsAccumulatedAndError(t *testing.T) {
	t.Parallel()

	input := append(dockerLogFrame(1, []byte("complete\n")), dockerLogHeader(1, uint32(len("partial\n")))...)
	input = append(input, []byte("part")...)

	got, err := DemuxLogStream(bytes.NewReader(input))
	if err == nil {
		t.Fatal("DemuxLogStream returned nil error for truncated payload")
	}
	if string(got) != "complete\npart" {
		t.Fatalf("DemuxLogStream = %q, want %q", string(got), "complete\npart")
	}
}

func TestDemuxLogStream_OversizedFrameSkipErrorReturnsAccumulated(t *testing.T) {
	t.Parallel()

	skipErr := errors.New("skip failed")
	reader := io.MultiReader(
		bytes.NewReader(dockerLogFrame(1, []byte("before\n"))),
		bytes.NewReader(dockerLogHeader(1, uint32(maxLogFrameSize+1))),
		strings.NewReader("short"),
		errReader{err: skipErr},
	)

	got, err := DemuxLogStream(reader)
	if !errors.Is(err, skipErr) {
		t.Fatalf("DemuxLogStream error = %v, want wrapping %v", err, skipErr)
	}
	if string(got) != "before\n" {
		t.Fatalf("DemuxLogStream = %q, want %q", string(got), "before\n")
	}
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

type bytesThenErrReader struct {
	data []byte
	err  error
}

func (r *bytesThenErrReader) Read(p []byte) (int, error) {
	if len(r.data) > 0 {
		n := copy(p, r.data)
		r.data = r.data[n:]
		return n, nil
	}
	return 0, r.err
}

type errReader struct {
	err error
}

func (r errReader) Read([]byte) (int, error) {
	return 0, r.err
}
