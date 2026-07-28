package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMockDockerCreateAndStartExec(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMockDockerHandler())
	defer server.Close()

	createResponse, err := http.Post(
		server.URL+"/v1.44/containers/c0000000001/exec",
		"application/json",
		strings.NewReader(`{"Cmd":["sh"],"Tty":true}`),
	)
	if err != nil {
		t.Fatalf("create exec: %v", err)
	}
	defer createResponse.Body.Close()
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create exec status = %d, want %d", createResponse.StatusCode, http.StatusCreated)
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode create exec: %v", err)
	}
	if created.ID == "" {
		t.Fatal("create exec returned an empty ID")
	}

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	conn, err := net.Dial("tcp", serverURL.Host)
	if err != nil {
		t.Fatalf("dial mock Docker: %v", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}

	body := `{"Detach":false,"Tty":true}`
	request := fmt.Sprintf(
		"POST /v1.44/exec/%s/start HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: tcp\r\nContent-Type: application/json\r\nContent-Length: %d\r\n\r\n%s",
		created.ID,
		len(body),
		body,
	)
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatalf("write start exec request: %v", err)
	}

	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("read start exec response: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("start exec status = %d, want %d", response.StatusCode, http.StatusSwitchingProtocols)
	}
	output := make([]byte, len("mock exec ready\n"))
	if _, err := io.ReadFull(reader, output); err != nil {
		t.Fatalf("read exec output: %v", err)
	}
	if string(output) != "mock exec ready\n" {
		t.Fatalf("exec output = %q, want %q", output, "mock exec ready\n")
	}
}

func TestMockDockerFollowLogsStreamsMultipleFrames(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMockDockerHandler())
	defer server.Close()

	response, err := http.Get(server.URL + "/v1.44/containers/c0000000001/logs?follow=1")
	if err != nil {
		t.Fatalf("get follow logs: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("logs status = %d, want 200", response.StatusCode)
	}

	for frame := 0; frame < 2; frame++ {
		header := make([]byte, 8)
		if _, err := io.ReadFull(response.Body, header); err != nil {
			t.Fatalf("read frame %d header: %v", frame, err)
		}
		size := binary.BigEndian.Uint32(header[4:8])
		if size == 0 {
			t.Fatalf("frame %d has empty payload", frame)
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(response.Body, payload); err != nil {
			t.Fatalf("read frame %d payload: %v", frame, err)
		}
	}
}

func TestMockDockerInventoryCarriesDockerImageIDs(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMockDockerHandler())
	defer server.Close()

	response, err := http.Get(server.URL + "/v1.44/containers/json")
	if err != nil {
		t.Fatalf("get containers: %v", err)
	}
	defer response.Body.Close()

	var containers []struct {
		ID      string `json:"Id"`
		ImageID string `json:"ImageID"`
	}
	if err := json.NewDecoder(response.Body).Decode(&containers); err != nil {
		t.Fatalf("decode containers: %v", err)
	}
	if len(containers) == 0 {
		t.Fatal("mock Docker returned no containers")
	}
	for _, container := range containers {
		if !strings.HasPrefix(container.ImageID, "sha256:") {
			t.Fatalf("container %q ImageID = %q, want sha256 ID", container.ID, container.ImageID)
		}
	}

	inspectResponse, err := http.Get(
		server.URL + "/v1.44/containers/" + containers[0].ID + "/json",
	)
	if err != nil {
		t.Fatalf("inspect container: %v", err)
	}
	defer inspectResponse.Body.Close()
	var inspect struct {
		Image string `json:"Image"`
	}
	if err := json.NewDecoder(inspectResponse.Body).Decode(&inspect); err != nil {
		t.Fatalf("decode inspect: %v", err)
	}
	if !strings.HasPrefix(inspect.Image, "sha256:") {
		t.Fatalf("inspect Image = %q, want sha256 ID", inspect.Image)
	}
}

func TestEncodeLogFrameSizeRejectsUint32Overflow(t *testing.T) {
	t.Parallel()

	header := make([]byte, 8)
	if err := encodeLogFrameSize(header, 1234); err != nil {
		t.Fatalf("encode valid size: %v", err)
	}
	if got := binary.BigEndian.Uint32(header[4:8]); got != 1234 {
		t.Fatalf("encoded size = %d, want 1234", got)
	}

	if strconv.IntSize == 64 {
		oversized := int(uint64(math.MaxUint32) + 1)
		if err := encodeLogFrameSize(header, oversized); err == nil {
			t.Fatal("expected uint32 overflow to be rejected")
		}
	}
}
