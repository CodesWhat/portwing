// SPDX-License-Identifier: MIT

//go:build integration

package integration

// dr_roundtrip_test.go drives the Standard-mode server end to end against the
// real daemon: the Docker proxy passthrough, a hijacked attach, and a compose
// up/down through the HTTP endpoint.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// proxyContainerIDs lists container IDs through Portwing's transparent proxy.
func proxyContainerIDs(t *testing.T, base string) []string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/containers/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /containers/json: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /containers/json: status %d: %s", resp.StatusCode, body)
	}
	var list []struct {
		ID string `json:"Id"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode container list: %v\n%s", err, body)
	}
	ids := make([]string, 0, len(list))
	for _, c := range list {
		ids = append(ids, c.ID)
	}
	return ids
}

// TestProxyPassthroughListsContainers: a request to the Docker API surface is
// forwarded to the daemon and its answer comes back untouched, and the proxy
// still refuses an unauthenticated caller.
func TestProxyPassthroughListsContainers(t *testing.T) {
	base, stop := startServer(t)
	defer stop()
	ctrID, cleanup := startAlpineContainer(t)
	defer cleanup()

	found := false
	for _, id := range proxyContainerIDs(t, base) {
		if id == ctrID {
			found = true
		}
	}
	if !found {
		t.Fatalf("container %s missing from the proxied /containers/json", ctrID)
	}

	resp, err := http.Get(base + "/containers/json") //nolint:noctx
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated proxy request: status %d, want 401", resp.StatusCode)
	}
}

// TestHijackAttachRoundTrip attaches to a running container through Portwing's
// hijack proxy, writes a line to its stdin and reads the container's answer
// back over the same upgraded connection.
func TestHijackAttachRoundTrip(t *testing.T) {
	base, stop := startServer(t)
	defer stop()

	out, err := exec.Command("docker", "run", "-d", "-it", testImage, "sh", "-c", "read l; echo got:$l; sleep 60").CombinedOutput()
	if err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}
	ctrID := strings.TrimSpace(string(out))
	defer exec.Command("docker", "rm", "-f", ctrID).Run() //nolint:errcheck

	addr := strings.TrimPrefix(base, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial Portwing: %v", err)
	}
	defer conn.Close()

	rawReq := "POST /containers/" + ctrID + "/attach?stream=1&stdin=1&stdout=1&stderr=1 HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Authorization: Bearer " + testToken + "\r\n" +
		"Connection: Upgrade\r\nUpgrade: tcp\r\nContent-Length: 0\r\n\r\n"
	if _, err := io.WriteString(conn, rawReq); err != nil {
		t.Fatalf("write attach request: %v", err)
	}
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	br := bufio.NewReader(conn)
	hr, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read attach response: %v", err)
	}
	_ = hr.Body.Close()
	if hr.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("attach status = %d, want 101", hr.StatusCode)
	}

	if _, err := io.WriteString(conn, "hello\n"); err != nil {
		t.Fatalf("write to attached stdin: %v", err)
	}
	var seen bytes.Buffer
	buf := make([]byte, 512)
	for !bytes.Contains(seen.Bytes(), []byte("got:hello")) {
		n, err := br.Read(buf)
		seen.Write(buf[:n])
		if err != nil {
			t.Fatalf("reading attached output: %v (saw %q)", err, seen.String())
		}
	}
}

// postCompose sends one compose request to the server's compose endpoint.
func postCompose(t *testing.T, base string, req map[string]any) (success bool, output string) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	hr, err := http.NewRequest(http.MethodPost, base+"/_portwing/compose", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	hr.Header.Set("Authorization", "Bearer "+testToken)
	hr.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(hr)
	if err != nil {
		t.Fatalf("POST /_portwing/compose %v: %v", req["operation"], err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("compose %v: status %d: %s", req["operation"], resp.StatusCode, raw)
	}
	var parsed struct {
		Success bool   `json:"success"`
		Output  string `json:"output"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("decode compose response: %v\n%s", err, raw)
	}
	if !parsed.Success {
		t.Fatalf("compose %v failed: %s\n%s", req["operation"], parsed.Error, parsed.Output)
	}
	return parsed.Success, parsed.Output
}

// TestComposeUpDownRoundTripOverHTTP deploys a stack through the compose
// endpoint (files shipped in the request), checks its containers run under the
// stack directory, then takes it down and checks they are gone.
func TestComposeUpDownRoundTripOverHTTP(t *testing.T) {
	h := newComposeHarness(t)
	stacks, _ := h.stacksDir(false)
	rel := h.prefix + "rt"
	dir := filepath.Join(stacks, rel)
	h.dirs = append(h.dirs, dir)

	base, stop := startServerWithEnv(t, []string{
		"STACKS_DIR=" + stacks,
	}, testToken)
	defer stop()

	files := map[string]string{"compose.yaml": composeStackYAML}
	postCompose(t, base, map[string]any{"operation": "up", "stackName": rel, "files": files})

	running := h.containersFor(dir)
	if len(running) != 2 {
		t.Fatalf("after up: want 2 containers, got %+v", running)
	}
	for _, c := range running {
		if c.State != "running" {
			t.Fatalf("container %s is %s, want running", c.ID, c.State)
		}
	}
	if _, ps := postCompose(t, base, map[string]any{"operation": "ps", "stackName": rel}); !strings.Contains(ps, "web") || !strings.Contains(ps, "db") {
		t.Fatalf("ps did not list both services:\n%s", ps)
	}

	postCompose(t, base, map[string]any{"operation": "down", "stackName": rel})
	if left := h.containersFor(dir); len(left) != 0 {
		t.Fatalf("down left containers: %+v", left)
	}
}
