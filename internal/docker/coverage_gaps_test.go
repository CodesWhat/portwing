package docker

// coverage_gaps_test.go – tests that close the remaining statement-coverage
// gaps in client.go, compose.go, and events.go without modifying any
// production code.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// ---- negotiateAPIVersion: NewRequestWithContext error (nil context) ----

// TestNegotiateAPIVersion_NilContext forces the http.NewRequestWithContext error
// branch by passing a nil context.  The Go net/http package returns
// "net/http: nil Context" for a nil context regardless of the URL or method.
func TestNegotiateAPIVersion_NilContext(t *testing.T) {
	t.Parallel()

	c := &Client{apiVersion: "v1.44"}
	//nolint:staticcheck // nil context is intentional to exercise the error branch
	err := c.negotiateAPIVersion(nil) //nolint:staticcheck
	if err == nil {
		t.Fatal("expected error from nil context, got nil")
	}
}

// ---- Ping: NewRequestWithContext error (nil context) ----

// TestPing_NilContext forces the http.NewRequestWithContext error branch
// in Ping by passing a nil context.
func TestPing_NilContext(t *testing.T) {
	t.Parallel()

	c := &Client{apiVersion: "v1.44"}
	//nolint:staticcheck // nil context is intentional to exercise the error branch
	err := c.Ping(nil) //nolint:staticcheck
	if err == nil {
		t.Fatal("expected error from nil context, got nil")
	}
}

// ---- writeStackFiles: resolvePath error for .env.drydock ----
// The resolvePath(".env.drydock") call inside writeStackFiles can only fail
// if the ComposeManager has an empty stacksDir combined with a
// stackDir/stackName that causes resolveStackRoot to error.  We trigger this
// by supplying a StackDir that is an absolute path (rejected by
// resolveStackRoot before ".env.drydock" is even resolved).
//
// Note: validateRequest would normally catch this first, so we call
// writeStackFiles directly.
func TestWriteStackFiles_ResolveEnvPathError(t *testing.T) {
	t.Parallel()

	cm := &ComposeManager{stacksDir: t.TempDir()}

	// StackDir is absolute → resolveStackRoot returns an error →
	// resolvePath used to build the file path fails →
	// writeStackFiles returns the "resolving path" error.
	req := ComposeRequest{
		StackName: "ignored",
		StackDir:  "/absolute/path",
		Files: map[string]string{
			// At least one file so we enter the Files loop which will call
			// resolvePath for the absolute stackDir and error.
			"docker-compose.yml": "services: {}\n",
		},
	}

	if err := cm.writeStackFiles(req); err == nil {
		t.Fatal("expected error from absolute StackDir in writeStackFiles, got nil")
	}
}

// TestWriteStackFiles_ResolveEnvDrydockPathError exercises the specific branch
// where EnvVars are set but the resolvePath(".env.drydock") call fails.
// We achieve this by using an absolute StackDir so resolveStackRoot errors
// when resolvePath is called for .env.drydock.
func TestWriteStackFiles_EnvVarResolvePathError(t *testing.T) {
	t.Parallel()

	cm := &ComposeManager{stacksDir: t.TempDir()}

	// Files is nil so we skip the file-writing loop.
	// EnvVars is non-empty so we enter the .env.drydock writing branch.
	// StackDir is absolute → resolvePath(".env.drydock") → resolveStackRoot errors.
	req := ComposeRequest{
		StackName: "ignored",
		StackDir:  "/absolute/path",
		EnvVars: map[string]string{
			"MY_VAR": "value",
		},
	}

	if err := cm.writeStackFiles(req); err == nil {
		t.Fatal("expected error from absolute StackDir when writing .env.drydock, got nil")
	}
}

// ---- readEvents: ctx.Err() at top of inner decode loop ----

// TestReadEvents_CtxErrAtLoopTop hits the ctx.Err() check at the very top of
// the inner for-loop in readEvents (before each Decode call).
//
// Strategy: the server pre-writes a large batch of non-allowed events into
// the response buffer before the client begins reading.  While readEvents
// iterates with `continue` through each non-allowed event, we cancel the
// context.  Eventually the `ctx.Err() != nil` check at the top of the loop
// fires before the next Decode call.
func TestReadEvents_CtxErrAtLoopTop(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// readyToCancel is closed once the server has flushed all events.
	readyToCancel := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		enc := json.NewEncoder(w)
		// Write many non-allowed events so the client spends time iterating
		// through them with `continue`.  The context is cancelled while the
		// client is in this hot loop, ensuring ctx.Err() fires at the loop top.
		const count = 500
		for i := 0; i < count; i++ {
			enc.Encode(DockerEvent{ID: "ctr1", Action: "exec_create", Type: "container"}) //nolint:errcheck
		}
		if flusher != nil {
			flusher.Flush()
		}
		close(readyToCancel)

		// Keep the connection open so readEvents doesn't get an EOF before
		// we've had a chance to trigger the ctx.Err() path.
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()

	c := newTestClient(srv)
	es := &EventStream{
		client:       c,
		initialDelay: 10 * time.Millisecond,
		maxDelay:     10 * time.Millisecond,
	}

	ch := make(chan DockerEvent, 8)
	done := make(chan error, 1)
	go func() {
		done <- es.readEvents(ctx, ch)
	}()

	// Cancel the context once the server has sent all events so that the
	// client goroutine sees the cancellation during its continue-loop.
	go func() {
		<-readyToCancel
		cancel()
	}()

	select {
	case err := <-done:
		// readEvents should return ctx.Err() (non-nil) after hitting the
		// ctx.Err() check at the top of the decode loop.
		if err == nil {
			t.Fatal("expected non-nil error (context cancelled), got nil")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for readEvents to return")
	}
}
