//go:build integration

package edge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/protocol"
)

func TestInteractiveExecAgainstDocker(t *testing.T) {
	socketPath := integrationDockerSocket(t)
	containerID := startIntegrationContainer(t, socketPath)

	dockerClient, err := docker.NewClient(socketPath, 10)
	if err != nil {
		t.Fatalf("create Docker client: %v", err)
	}
	client, controller := newTestClient(t)
	client.dockerClient = dockerClient

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const sessionID = "edge-integration-exec"
	tty := true
	client.StartExec(ctx, protocol.ExecStartMessage{
		ExecID:      sessionID,
		ContainerID: containerID,
		Cmd: []string{
			"sh",
			"-c",
			`IFS= read -r line; stty size; printf 'edge-sentinel:%s\n' "$line"`,
		},
		Cols: 80,
		Rows: 24,
		Tty:  &tty,
	})

	var output bytes.Buffer
	ready := false
	for {
		envelope := expectIntegrationEnvelope(t, controller)
		switch envelope.Type {
		case protocol.TypeExecReady:
			if ready {
				t.Fatal("received duplicate exec_ready")
			}
			var message protocol.ExecReadyMessage
			decodeData(t, envelope.Data, &message)
			if message.ExecID != sessionID {
				t.Fatalf("exec_ready ID = %q, want %q", message.ExecID, sessionID)
			}
			ready = true
			client.HandleResize(ctx, protocol.ExecResizeMessage{ExecID: sessionID, Cols: 101, Rows: 41})
			client.HandleInput(protocol.ExecInputMessage{
				ExecID: sessionID,
				Data:   base64.StdEncoding.EncodeToString([]byte("edge-input\n")),
			})

		case protocol.TypeExecOutput:
			if !ready {
				t.Fatal("received exec_output before exec_ready")
			}
			var message protocol.ExecOutputMessage
			decodeData(t, envelope.Data, &message)
			if message.ExecID != sessionID {
				t.Fatalf("exec_output ID = %q, want %q", message.ExecID, sessionID)
			}
			chunk, err := base64.StdEncoding.DecodeString(message.Data)
			if err != nil {
				t.Fatalf("decode exec output: %v", err)
			}
			output.Write(chunk)

		case protocol.TypeExecEnd:
			var message protocol.ExecEndMessage
			decodeData(t, envelope.Data, &message)
			if message.ExecID != sessionID || message.Reason != "exited" {
				t.Fatalf("exec_end = %+v, want %q / exited", message, sessionID)
			}
			if !ready {
				t.Fatal("received exec_end before exec_ready")
			}
			got := output.String()
			if !strings.Contains(got, "41 101") {
				t.Fatalf("TTY output %q does not contain resized dimensions 41 101", got)
			}
			if !strings.Contains(got, "edge-sentinel:edge-input") {
				t.Fatalf("TTY output %q does not contain input sentinel", got)
			}
			waitFor(t, "completed exec session cleanup", func() bool {
				_, exists := client.execSessions.Load(sessionID)
				return !exists
			})
			return

		default:
			t.Fatalf("unexpected edge exec envelope %q: %s", envelope.Type, envelope.Data)
		}
	}
}

func integrationDockerSocket(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker command is not available")
	}
	socketPath := os.Getenv("PORTWING_TEST_DOCKER_SOCKET")
	if socketPath == "" {
		socketPath = "/var/run/docker.sock"
	}
	if _, err := os.Stat(socketPath); err != nil {
		t.Skipf("Docker socket %s is not available: %v", socketPath, err)
	}
	return socketPath
}

func startIntegrationContainer(t *testing.T, socketPath string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	host := "unix://" + socketPath
	command := exec.CommandContext(ctx, "docker", "--host", host, "run", "-d", "alpine:3.20", "sleep", "300")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("start integration container: %v\n%s", err, output)
	}
	containerID := strings.TrimSpace(string(output))
	if containerID == "" {
		t.Fatal("docker run returned an empty container ID")
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		output, err := exec.CommandContext(cleanupCtx, "docker", "--host", host, "rm", "-f", containerID).CombinedOutput()
		if err != nil {
			t.Errorf("remove integration container %s: %v\n%s", containerID, err, output)
		}
	})
	return containerID
}

func expectIntegrationEnvelope(t *testing.T, controller *websocket.Conn) protocol.Envelope {
	t.Helper()
	if err := controller.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatalf("set controller read deadline: %v", err)
	}
	_, data, err := controller.ReadMessage()
	if err != nil {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatal("timed out waiting for edge exec envelope")
		}
		t.Fatalf("read edge exec envelope: %v", err)
	}
	var envelope protocol.Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode edge exec envelope %q: %v", data, err)
	}
	return envelope
}
