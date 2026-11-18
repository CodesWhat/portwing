// SPDX-License-Identifier: MIT

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

type capabilityListAdapter struct {
	stubServerAdapter
	capabilities []string
}

func (a *capabilityListAdapter) Capabilities() []string { return a.capabilities }

func TestInfoCapabilitiesPreserveFirstOccurrence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		adapter, want []string
	}{
		{"nil adapter", nil, []string{"compose", "exec", "metrics", "events"}},
		{"overlap", []string{"events", "future", "exec", "future", "Events"}, []string{"compose", "exec", "metrics", "events", "future", "Events"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, stop := newStubDockerClient(t)
			defer stop()
			s, err := NewServer(minimalConfig(), client, &capabilityListAdapter{capabilities: tc.adapter})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := s.Shutdown(context.Background()); err != nil {
					t.Error(err)
				}
			}()
			response := httptest.NewRecorder()
			s.httpServer.Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://localhost/_portwing/info", nil))
			if response.Code != http.StatusOK {
				t.Fatalf("HTTP%d: %s", response.Code, response.Body)
			}
			var body struct {
				Capabilities []string `json:"capabilities"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(body.Capabilities, tc.want) {
				t.Fatalf("capabilities=%v, want%v", body.Capabilities, tc.want)
			}
		})
	}
}
