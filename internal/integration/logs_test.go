// SPDX-License-Identifier: MIT

//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestContainerLogsPreserveBinaryPrefix(t *testing.T) {
	base, stop := startServer(t)
	defer stop()
	want := []byte{1, 0, 0, 0, 0, 0, 0, 5, 'h', 'e', 'l', 'l', 'o'}
	for _, tty := range []bool{false, true} {
		t.Run(fmt.Sprintf("tty=%v", tty), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			args := []string{"create"}
			if tty {
				args = append(args, "--tty")
			}
			args = append(args, testImage, "sh", "-c", `printf '\001\000\000\000\000\000\000\005hello'`)
			create := exec.CommandContext(ctx, "docker", args...)
			var diagnostics bytes.Buffer
			create.Stderr = &diagnostics
			out, err := create.Output()
			if err != nil {
				t.Fatalf("create log fixture: %v: %s", err, diagnostics.String())
			}
			id := strings.TrimSpace(string(out))
			t.Cleanup(func() {
				cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cleanupCancel()
				if out, err := exec.CommandContext(cleanupCtx, "docker", "rm", "-f", id).CombinedOutput(); err != nil {
					t.Errorf("remove log fixture %s: %v: %s", id, err, out)
				}
			})
			for _, action := range []string{"start", "wait"} {
				out, err := exec.CommandContext(ctx, "docker", action, id).CombinedOutput()
				if err != nil {
					t.Fatalf("%s log fixture: %v: %s", action, err, out)
				}
				if action == "wait" && strings.TrimSpace(string(out)) != "0" {
					t.Fatalf("log fixture exit status: %s", out)
				}
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/containers/"+id+"/logs", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+testToken)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			got, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
			if err != nil || resp.StatusCode != http.StatusOK || !bytes.Equal(got, want) {
				t.Fatalf("logs: status %d, bytes %v, error %v; want %v", resp.StatusCode, got, err, want)
			}
		})
	}
}
