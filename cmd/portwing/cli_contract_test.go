// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/adapter/drydock"
	"github.com/codeswhat/portwing/internal/config"
)

func TestRunSignatureLockMissingSubcommandShowsUsage(t *testing.T) {
	t.Setenv("TOKEN", "")
	t.Setenv("TOKEN_HASH", "invalid")
	var stdout, stderr bytes.Buffer
	code := run([]string{"portwing", "signature-lock"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "Usage: portwing signature-lock") {
		t.Fatalf("exit %d, stderr %q; want signature-lock usage and exit 2", code, stderr.String())
	}
}

func TestSelectAdapterReportsConfiguredPollingSeconds(t *testing.T) {
	client := newDockerClientForTest(t)
	a := selectAdapter(&config.Config{Adapter: "drydock", DDPollInterval: 17, LogLevel: "warn"}, client)
	d, ok := a.(*drydock.Adapter)
	if !ok {
		t.Fatalf("adapter = %T", a)
	}
	srv := httptest.NewServer(d.SSE())
	defer srv.Close()
	httpClient := srv.Client()
	httpClient.Timeout = 2 * time.Second
	response, err := httpClient.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var ack struct {
		Type string `json:"type"`
		Data struct {
			PollInterval string `json:"pollInterval"`
			LogLevel     string `json:"logLevel"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ack); err != nil {
		t.Fatal(err)
	}
	if ack.Type != "dd:ack" || ack.Data.PollInterval != "17s" || ack.Data.LogLevel != "warn" {
		t.Fatalf("ack = %+v", ack)
	}
}
