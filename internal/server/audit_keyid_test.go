// SPDX-License-Identifier: MIT

package server

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/auth"
	"github.com/codeswhat/portwing/internal/docker"
)

// TestAuditRecordsAttributeSigningKey sends requests signed by two different
// Ed25519 keys from one IP and expects api_request, compose_op and exec_start
// records to carry each caller's own key_id in the ring, the sink and the
// export stream. WA-AUDIT-KEYID.
func TestAuditRecordsAttributeSigningKey(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var lines strings.Builder
	privs := make([]ed25519.PrivateKey, 2)
	ids := make([]string, 2)
	for i := range privs {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("GenerateKey: %v", err)
		}
		privs[i] = priv
		ids[i] = auth.KeyIDForPublicKey(pub)
		lines.WriteString("ed25519 " + base64.StdEncoding.EncodeToString(pub) + " key\n")
	}
	keysPath := filepath.Join(dir, "authorized_keys")
	if err := os.WriteFile(keysPath, []byte(lines.String()), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	registry := auth.NewKeyRegistry(keysPath)
	if err := registry.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	ed := Ed25519Config{Registry: registry, Nonces: auth.NewNonceLRU(1000, 60), MaxSkewSeconds: 60}

	sinkPath := filepath.Join(dir, "audit.log")
	auditor, closeAudit, err := audit.New(sinkPath, 64)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	defer closeAudit()

	sockPath, cleanup := shortSocketPath(t)
	defer cleanup()
	rl := NewRateLimiter()
	defer rl.Stop()
	s := &Server{
		compose:     docker.NewComposeManager("", "1.44", sockPath),
		rateLimiter: rl,
		auditor:     auditor,
		cfg:         minimalConfig(),
	}

	h := rl.AuthMiddlewareWithEd25519(nil, ed, auditor, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.auditExecStart(r, true)
		s.handleCompose(w, r)
	}))
	for _, priv := range privs {
		body := []byte(`{"operation":"up","stackName":"mystack"}`)
		req := httptest.NewRequest(http.MethodPost, "/_portwing/compose", bytes.NewReader(body))
		req.RemoteAddr = "192.0.2.7:4000"
		signEd25519Request(t, req, body, priv, time.Now().Unix(), freshNonce(t))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
	}

	wantEvents := []string{audit.EventExecStart, audit.EventComposeOp, audit.EventAPIRequest}
	check := func(source string, records []audit.Record) {
		t.Helper()
		got := map[string]map[string]bool{}
		for _, r := range records {
			if got[r.Event] == nil {
				got[r.Event] = map[string]bool{}
			}
			if r.KeyID == "" {
				t.Errorf("%s: %s record has no key_id: %+v", source, r.Event, r)
			}
			got[r.Event][r.KeyID] = true
		}
		for _, ev := range wantEvents {
			for _, id := range ids {
				if !got[ev][id] {
					t.Errorf("%s: %s records missing key_id %s (have %v)", source, ev, id, got[ev])
				}
			}
		}
	}

	check("ring", auditor.Records(0))

	exportReq := httptest.NewRequest(http.MethodGet, "/_portwing/audit/export", nil)
	exportRec := httptest.NewRecorder()
	s.handleAuditExport(exportRec, exportReq)
	var exported []audit.Record
	sc := bufio.NewScanner(exportRec.Body)
	for sc.Scan() {
		var rec audit.Record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("decode export line: %v", err)
		}
		exported = append(exported, rec)
	}
	check("export", exported)

	sink, err := os.ReadFile(sinkPath)
	if err != nil {
		t.Fatalf("ReadFile sink: %v", err)
	}
	var sinkRecords []audit.Record
	for _, line := range bytes.Split(bytes.TrimSpace(sink), []byte("\n")) {
		var raw struct {
			Event string `json:"event"`
			KeyID string `json:"key_id"`
		}
		if err := json.Unmarshal(line, &raw); err != nil {
			t.Fatalf("decode sink line %q: %v", line, err)
		}
		sinkRecords = append(sinkRecords, audit.Record{Event: raw.Event, KeyID: raw.KeyID})
	}
	check("sink", sinkRecords)
}

// TestAuditRecordsOmitKeyIDForTokenCallers keeps shared-token traffic
// unattributed.
func TestAuditRecordsOmitKeyIDForTokenCallers(t *testing.T) {
	t.Parallel()
	auditor, closeAudit, err := audit.New("", 8)
	if err != nil {
		t.Fatalf("audit.New: %v", err)
	}
	defer closeAudit()
	rl := NewRateLimiter()
	defer rl.Stop()
	s := &Server{rateLimiter: rl, auditor: auditor}
	req := httptest.NewRequest(http.MethodPost, "/exec/abc/start", nil)
	req.RemoteAddr = "192.0.2.7:4000"
	s.auditExecStart(req, true)
	records := auditor.Records(0)
	if len(records) != 1 || records[0].KeyID != "" {
		t.Fatalf("records = %+v, want one record without key_id", records)
	}
}
