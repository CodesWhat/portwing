package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/auth"
)

type blockingEnrollmentBody struct {
	id      string
	entered chan<- string
	release <-chan struct{}
	once    sync.Once
}

func (b *blockingEnrollmentBody) Read([]byte) (int, error) {
	b.once.Do(func() { b.entered <- b.id })
	<-b.release
	return 0, io.ErrUnexpectedEOF
}

func (*blockingEnrollmentBody) Close() error { return nil }

func TestRateLimitOnlyCapsConcurrentEnrollmentBodiesAndReleasesAdmission(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Stop()
	rl.maxInFlight = 2

	entered := make(chan string, 4)
	releases := map[string]chan struct{}{
		"first":  make(chan struct{}),
		"second": make(chan struct{}),
		"third":  make(chan struct{}),
		"fourth": make(chan struct{}),
	}
	releaseOnce := map[string]*sync.Once{
		"first":  {},
		"second": {},
		"third":  {},
		"fourth": {},
	}
	release := func(id string) {
		releaseOnce[id].Do(func() { close(releases[id]) })
	}
	defer func() {
		for id := range releases {
			release(id)
		}
	}()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	keyPath := writeAuthorizedKeys(t, pub)
	registry := auth.NewKeyRegistry(keyPath)
	if err := registry.Load(); err != nil {
		t.Fatalf("load key registry: %v", err)
	}
	enroller := auth.NewEnroller("expected", keyPath, registry)
	h := rl.rateLimitOnly(enroller, nil)

	serve := func(id string) <-chan int {
		result := make(chan int, 1)
		go func() {
			body := &blockingEnrollmentBody{id: id, entered: entered, release: releases[id]}
			req := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", body)
			req.RemoteAddr = "192.0.2.20:4444"
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			result <- rec.Code
		}()
		return result
	}
	waitEntered := func(want string) {
		t.Helper()
		select {
		case got := <-entered:
			if got != want {
				t.Fatalf("request %q entered while waiting for %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("request %q was not admitted", want)
		}
	}
	waitStatus := func(result <-chan int, want int) {
		t.Helper()
		select {
		case got := <-result:
			if got != want {
				t.Fatalf("response status = %d, want %d", got, want)
			}
		case <-time.After(time.Second):
			t.Fatal("request did not finish")
		}
	}

	first := serve("first")
	waitEntered("first")
	second := serve("second")
	waitEntered("second")

	third := serve("third")
	select {
	case got := <-third:
		if got != http.StatusTooManyRequests {
			release("first")
			release("second")
			t.Fatalf("third concurrent enrollment status = %d, want 429", got)
		}
	case got := <-entered:
		release("first")
		release("second")
		release("third")
		waitStatus(first, http.StatusBadRequest)
		waitStatus(second, http.StatusBadRequest)
		waitStatus(third, http.StatusBadRequest)
		t.Fatalf("request %q entered after the per-client enrollment capacity was full", got)
	case <-time.After(time.Second):
		release("first")
		release("second")
		release("third")
		t.Fatal("third concurrent enrollment neither entered nor received 429")
	}

	release("first")
	waitStatus(first, http.StatusBadRequest)

	fourth := serve("fourth")
	waitEntered("fourth")
	release("second")
	release("fourth")
	waitStatus(second, http.StatusBadRequest)
	waitStatus(fourth, http.StatusBadRequest)
}

func TestRateLimitOnlyCapsEnrollmentBodiesAcrossClients(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Stop()
	rl.maxInFlight = 2
	rl.maxEnrollmentInFlight = 2

	entered := make(chan string, 3)
	release := make(chan struct{})
	h := rl.rateLimitOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- r.RemoteAddr
		<-release
		http.Error(w, "invalid JSON", http.StatusBadRequest)
	}), nil)

	serve := func(remoteAddr string) <-chan int {
		result := make(chan int, 1)
		go func() {
			req := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", nil)
			req.RemoteAddr = remoteAddr
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			result <- rec.Code
		}()
		return result
	}

	first := serve("192.0.2.1:4000")
	second := serve("192.0.2.2:4000")
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("enrollment request was not admitted")
		}
	}

	third := serve("192.0.2.3:4000")
	select {
	case got := <-third:
		if got != http.StatusTooManyRequests {
			close(release)
			t.Fatalf("third client status = %d, want 429", got)
		}
	case got := <-entered:
		close(release)
		t.Fatalf("third client %q entered after the global enrollment capacity was full", got)
	case <-time.After(time.Second):
		close(release)
		t.Fatal("third client neither entered nor received 429")
	}

	close(release)
	for i, result := range []<-chan int{first, second} {
		select {
		case got := <-result:
			if got != http.StatusBadRequest {
				t.Fatalf("admitted request %d status = %d, want 400", i+1, got)
			}
		case <-time.After(time.Second):
			t.Fatalf("admitted request %d did not finish", i+1)
		}
	}
}

func TestRateLimitOnlyReleasesEnrollmentAdmissionAfterPanic(t *testing.T) {
	rl := NewRateLimiter()
	defer rl.Stop()
	rl.maxInFlight = 1
	rl.maxEnrollmentInFlight = 1

	panicking := rl.rateLimitOnly(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}), nil)
	req := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", nil)
	req.RemoteAddr = "192.0.2.1:4000"
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected downstream panic")
			}
		}()
		panicking.ServeHTTP(httptest.NewRecorder(), req)
	}()

	ok := rl.rateLimitOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), nil)
	rec := httptest.NewRecorder()
	ok.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("request after panic status = %d, want 204", rec.Code)
	}
}

func TestEnrollmentAuditActorUsesTrustedClientResolution(t *testing.T) {
	tests := []struct {
		name           string
		trustedProxies []string
		remoteAddr     string
		forwardedFor   string
		wantActor      string
	}{
		{
			name:         "direct peer by default",
			remoteAddr:   "192.0.2.10:4444",
			forwardedFor: "198.51.100.7",
			wantActor:    "192.0.2.10",
		},
		{
			name:           "forwarded client behind trusted proxies",
			trustedProxies: []string{"10.0.0.0/8"},
			remoteAddr:     "10.0.0.9:4444",
			forwardedFor:   "198.51.100.7, 10.0.0.8",
			wantActor:      "198.51.100.7",
		},
		{
			name:           "forwarding header from untrusted peer",
			trustedProxies: []string{"10.0.0.0/8"},
			remoteAddr:     "192.0.2.10:4444",
			forwardedFor:   "198.51.100.7",
			wantActor:      "192.0.2.10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub, _, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatalf("GenerateKey: %v", err)
			}
			keyPath := writeAuthorizedKeys(t, pub)
			client, stopClient := newStubDockerClient(t)
			t.Cleanup(stopClient)

			cfg := minimalConfig()
			cfg.AuthorizedKeysFile = keyPath
			cfg.EnrollmentToken = "expected"
			cfg.TrustedProxies = tt.trustedProxies
			s, err := NewServer(cfg, client, &stubServerAdapter{})
			if err != nil {
				t.Fatalf("NewServer: %v", err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := s.Shutdown(ctx); err != nil {
					t.Errorf("Shutdown: %v", err)
				}
			})

			body := `{"enrollment_token":"wrong","public_key":""}`
			req := httptest.NewRequest(http.MethodPost, "/api/portwing/enroll", strings.NewReader(body))
			req.RemoteAddr = tt.remoteAddr
			req.Header.Set("X-Forwarded-For", tt.forwardedFor)
			rec := httptest.NewRecorder()
			s.httpServer.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("enrollment status = %d, want 401: %s", rec.Code, rec.Body.String())
			}
			records := s.auditor.Records(0)
			if len(records) != 1 || records[0].Event != audit.EventEnrollment {
				t.Fatalf("enrollment audit records = %+v, want one enrollment event", records)
			}
			if got := records[0].Actor; got != tt.wantActor {
				t.Fatalf("enrollment audit actor = %q, want %q", got, tt.wantActor)
			}
		})
	}
}
