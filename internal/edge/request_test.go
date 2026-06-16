package edge

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/codeswhat/portwing/internal/protocol"
)

func mkResp(status int, contentType, body string) *http.Response {
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// A non-streaming request is proxied via Do and returned as a single response
// envelope carrying the status, content type, and body.
func TestHandleRequestNonStream(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	//nolint:bodyclose // the response body is consumed and closed by handleRequest, the code under test.
	fd := &fakeDocker{doResp: mkResp(http.StatusCreated, "application/json", `{"ok":true}`)}
	c.dockerClient = fd

	c.handleRequest(context.Background(), protocol.RequestMessage{
		RequestID: "r1",
		Method:    "POST",
		Path:      "/containers/create",
	})

	var resp protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &resp)
	if resp.RequestID != "r1" {
		t.Errorf("RequestID = %q, want r1", resp.RequestID)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("StatusCode = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	if resp.IsStream {
		t.Error("IsStream = true, want false for a unary request")
	}
	if string(resp.Body) != `{"ok":true}` {
		t.Errorf("Body = %s, want {\"ok\":true}", resp.Body)
	}
	if resp.ContentType != "application/json" {
		t.Errorf("ContentType = %q, want application/json", resp.ContentType)
	}
}

// A request that fails at the Docker client is reported as an error envelope
// tagged with the originating request id.
func TestHandleRequestError(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	fd := &fakeDocker{doErr: errors.New("dial fail")}
	c.dockerClient = fd

	c.handleRequest(context.Background(), protocol.RequestMessage{
		RequestID: "r2",
		Method:    "GET",
		Path:      "/info",
	})

	var em protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &em)
	if em.RequestID != "r2" {
		t.Errorf("error RequestID = %q, want r2", em.RequestID)
	}
	if em.Message != "dial fail" {
		t.Errorf("error Message = %q, want dial fail", em.Message)
	}
}

// A streaming request is proxied via DoStream and tunneled as a stream-header
// response, one or more base64 stream chunks, and a terminal stream_end.
func TestHandleRequestStream(t *testing.T) {
	t.Parallel()

	c, ctrl := newTestClient(t)
	//nolint:bodyclose // the response body is consumed and closed by handleRequest, the code under test.
	fd := &fakeDocker{streamResp: mkResp(http.StatusOK, "application/octet-stream", "chunk-data")}
	c.dockerClient = fd

	c.handleRequest(context.Background(), protocol.RequestMessage{
		RequestID: "r3",
		Method:    "GET",
		Path:      "/containers/abc/logs?follow=1",
	})

	var resp protocol.ResponseMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeResponse), &resp)
	if !resp.IsStream {
		t.Error("IsStream = false, want true for a streaming path")
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}

	var chunk protocol.StreamMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeStream), &chunk)
	if chunk.RequestID != "r3" {
		t.Errorf("stream RequestID = %q, want r3", chunk.RequestID)
	}
	if decoded, _ := base64.StdEncoding.DecodeString(chunk.Data); string(decoded) != "chunk-data" {
		t.Errorf("stream payload = %q, want chunk-data", decoded)
	}

	var end protocol.StreamEndMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeStreamEnd), &end)
	if end.RequestID != "r3" || end.Reason != "complete" {
		t.Errorf("stream_end = %+v, want r3 / complete", end)
	}
}
