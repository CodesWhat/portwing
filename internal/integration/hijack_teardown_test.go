// SPDX-License-Identifier: MIT

//go:build integration

package integration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestDockerStopReturnsAfterControllerDropsExecSession starts an exec through
// Portwing's hijack proxy against the real daemon, drops the controller
// connection mid-session, and checks that "docker stop" still returns within
// its timeout and the container is gone.
func TestDockerStopReturnsAfterControllerDropsExecSession(t *testing.T) {
	base, stopServer := startServer(t)
	defer stopServer()
	ctrID, cleanupCtr := startAlpineContainer(t)
	defer cleanupCtr()

	// Create the exec instance through Portwing's Docker proxy.
	createBody := `{"AttachStdin":true,"AttachStdout":true,"AttachStderr":true,"Tty":false,"Cmd":["sh","-c","sleep 300"]}`
	req, err := http.NewRequest(http.MethodPost, base+"/containers/"+ctrID+"/exec", strings.NewReader(createBody))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create exec: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var created struct{ ID string }
	if err := json.Unmarshal(raw, &created); err != nil || created.ID == "" {
		t.Fatalf("create exec: status %d body %s", resp.StatusCode, raw)
	}

	// Start it over a raw hijacked connection.
	addr := strings.TrimPrefix(base, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial Portwing: %v", err)
	}
	defer conn.Close()
	startBody := `{"Detach":false,"Tty":false}`
	rawReq := "POST /exec/" + created.ID + "/start HTTP/1.1\r\nHost: " + addr + "\r\n" +
		"Authorization: Bearer " + testToken + "\r\n" +
		"Connection: Upgrade\r\nUpgrade: tcp\r\nContent-Type: application/json\r\n" +
		"Content-Length: " + strconv.Itoa(len(startBody)) + "\r\n\r\n" + startBody
	if _, err := io.WriteString(conn, rawReq); err != nil {
		t.Fatalf("write exec start: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	hr, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read exec start response: %v", err)
	}
	_ = hr.Body.Close()
	if hr.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("exec start status = %d, want 101", hr.StatusCode)
	}

	// Controller drops.
	_ = conn.Close()

	const stopTimeout = 5
	start := time.Now()
	cmd := exec.Command("docker", "stop", "-t", strconv.Itoa(stopTimeout), ctrID)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		t.Fatalf("docker stop: %v", err)
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("docker stop: %v\n%s", err, out.String())
		}
	case <-time.After((stopTimeout + 10) * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("docker stop did not return within %ds after the controller dropped", stopTimeout+10)
	}
	t.Logf("docker stop returned in %s", time.Since(start).Round(time.Millisecond))

	state, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", ctrID).CombinedOutput()
	if err != nil || strings.TrimSpace(string(state)) != "false" {
		t.Fatalf("container still running after stop: %v %s", err, state)
	}
}
