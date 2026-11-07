// SPDX-License-Identifier: MIT

package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResponseLogFormat(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, contentType, apiVersion string
		want                          logStreamFormat
	}{
		{"raw type at introduction", "application/vnd.docker.raw-stream", "v1.42", logFormatRaw},
		{"older ambiguous raw type", "application/vnd.docker.raw-stream", "v1.41", logFormatAuto},
		{"newer API major", "application/vnd.docker.raw-stream", "v2.0", logFormatRaw},
		{"older API major", "application/vnd.docker.raw-stream", "v0.99", logFormatAuto},
		{"missing version separator", "application/vnd.docker.raw-stream", "v1", logFormatAuto},
		{"invalid version major", "application/vnd.docker.raw-stream", "vbad.44", logFormatAuto},
		{"invalid version minor", "application/vnd.docker.raw-stream", "v1.bad", logFormatAuto},
		{"missing content type", "", "v1.44", logFormatAuto},
		{"unrecognized content type", "text/plain", "v1.44", logFormatAuto},
		{"unambiguous multiplexed type", "application/vnd.docker.multiplexed-stream", "v1.41", logFormatMultiplexed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := responseLogFormat(tc.contentType, tc.apiVersion); got != tc.want {
				t.Fatalf("format = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestGetContainerLogsPreservesRawContentType(t *testing.T) {
	t.Parallel()
	payload := []byte{1, 0, 0, 0, 0, 0, 0, 5, 'h', 'e', 'l', 'l', 'o'}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()
	client := newTestClient(srv)
	body, err := client.GetContainerLogs(context.Background(), "raw-tty", "", "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	got, err := DecodeContainerLogs(body)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("raw Docker response changed: got %v, err %v, want %v", got, err, payload)
	}
}

func TestDecodeContainerLogsRejectsTruncatedHeader(t *testing.T) {
	input := append([]byte{1, 0, 0, 0, 0, 0, 0, 5, 'h', 'e', 'l', 'l', 'o'}, 2, 0, 0)
	_, err := DecodeContainerLogs(bytes.NewReader(input))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated multiplexed header returned %v, want unexpected EOF", err)
	}
}

func TestDecodeContainerLogsPreservesLargeFrames(t *testing.T) {
	payload := bytes.Repeat([]byte{'x'}, maxLogFrameSize+1)
	input := append([]byte{1, 0, 0, 0, 0, 4, 0, 1}, payload...)
	got, err := DecodeContainerLogs(bytes.NewReader(input))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("large frame: got %d bytes, err %v, want %d bytes", len(got), err, len(payload))
	}
}

func TestContainerLogFormatSurvivesLimits(t *testing.T) {
	t.Parallel()
	frame := []byte{1, 0, 0, 0, 0, 0, 0, 5, 'h', 'e', 'l', 'l', 'o'}
	for _, tc := range []struct {
		name, apiVersion, contentType string
		input, want                   []byte
		limit                         int64
		wantError                     bool
	}{
		{"limited raw", "v1.44", "application/vnd.docker.raw-stream; charset=utf-8", frame, frame[:9], 9, false},
		{"explicit short multiplexed header", "v1.44", "application/vnd.docker.multiplexed-stream", frame[:3], nil, 3, true},
		{"legacy ambiguous content type", "v1.41", "application/vnd.docker.raw-stream", frame, []byte("hello"), int64(len(frame)), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				_, _ = w.Write(tc.input)
			}))
			defer srv.Close()
			client := newTestClient(srv)
			client.apiVersion = tc.apiVersion
			body, err := client.GetContainerLogs(context.Background(), "test", "", "", "", false, false)
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			got, err := DecodeContainerLogs(LimitContainerLogStream(body, tc.limit))
			if tc.wantError && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("error %v, want unexpected EOF", err)
			}
			if !tc.wantError && err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLargeLogFrameStopsOnConsumerError(t *testing.T) {
	payload := bytes.Repeat([]byte{'x'}, maxLogFrameSize+1)
	input := append([]byte{2, 0, 0, 0, 0, 4, 0, 1}, payload...)
	stop := errors.New("stop")
	calls := 0
	err := DecodeContainerLogStream(bytes.NewReader(input), func(ContainerLogStream, []byte) error {
		calls++
		return stop
	})
	if !errors.Is(err, stop) || calls != 1 {
		t.Fatalf("consumer calls %d, error %v", calls, err)
	}
}
