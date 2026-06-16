package edge

import (
	"context"
	"crypto/ed25519"
	crand "crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeswhat/portwing/internal/adapter"
	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/auth"
	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/docker"
	"github.com/codeswhat/portwing/internal/metrics"
	"github.com/codeswhat/portwing/internal/protocol"
)

const (
	maxReadSize     = 16 * 1024 * 1024  // 16 MB — WebSocket read limit
	maxResponseBody = 100 * 1024 * 1024 // 100 MB — proxied response body cap
	maxExecSessions = 100               // concurrent exec sessions
	maxStreams      = 100               // concurrent in-flight tunneled requests
)

// edgeMessageSender wraps the edge Client to implement adapter.MessageSender.
type edgeMessageSender struct {
	client *Client
}

func (s *edgeMessageSender) SendTypedMessage(msgType string, data interface{}) error {
	return s.client.sendTypedMessage(msgType, data)
}

// Client is the edge-mode WebSocket client that connects to a controller
// and tunnels Docker API requests over the WebSocket.
type Client struct {
	cfg          *config.Config
	dockerClient *docker.Client
	adapter      adapter.EdgeAdapter
	compose      *docker.ComposeManager
	collector    *metrics.Collector
	auditor      *audit.Logger

	conn   *websocket.Conn
	connMu sync.Mutex

	execSessions sync.Map

	// streamSem bounds concurrent in-flight request handlers (maxStreams).
	streamSem chan struct{}

	// Health server for Docker HEALTHCHECK.
	healthServer *http.Server
}

// NewClient creates a new edge-mode Client.
func NewClient(cfg *config.Config, dockerClient *docker.Client, a adapter.EdgeAdapter, auditor *audit.Logger) *Client {
	return &Client{
		cfg:          cfg,
		dockerClient: dockerClient,
		adapter:      a,
		compose:      docker.NewComposeManager(cfg.StacksDir, dockerClient.GetAPIVersion(), cfg.DockerSocket),
		collector:    metrics.NewCollector("/var/lib/docker", cfg.SkipDFCollection),
		auditor:      auditor,
		streamSem:    make(chan struct{}, maxStreams),
	}
}

// Run is the main loop. It starts a minimal health server and then enters a
// connect-retry loop with exponential backoff and jitter.
func (c *Client) Run(ctx context.Context) error {
	// Start minimal health HTTP server for Docker HEALTHCHECK.
	c.startHealthServer()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.healthServer.Shutdown(shutdownCtx); err != nil {
			slog.Warn("health server shutdown error", "error", err)
		}
	}()

	delay := time.Duration(c.cfg.ReconnectDelay) * time.Second
	maxDelay := time.Duration(c.cfg.MaxReconnectDelay) * time.Second

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		established, err := c.connect(ctx)
		if ctx.Err() != nil {
			// Shutting down - send close frame if we still have a connection.
			c.connMu.Lock()
			if c.conn != nil {
				// Best-effort close frame on shutdown; ignore send errors.
				_ = c.conn.WriteMessage(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "shutdown"),
				)
				closeWebSocket(c.conn, "shutdown")
				c.conn = nil
			}
			c.connMu.Unlock()
			return ctx.Err()
		}

		if err != nil {
			slog.Warn("connection lost", "error", err)
		}

		// Reset backoff after a connection that was actually established, so a
		// long-lived session that later drops reconnects from RECONNECT_DELAY
		// instead of inheriting stale backoff from earlier failures (SPEC §13.1).
		if established {
			delay = time.Duration(c.cfg.ReconnectDelay) * time.Second
		}

		waitDuration := jitteredDuration(delay)

		slog.Info("reconnecting", "delay", waitDuration.Round(time.Millisecond))

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitDuration):
		}

		// Exponential backoff.
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

// connect dials the WebSocket, performs the hello/welcome handshake, syncs
// state, and runs the read and write pumps.
func (c *Client) connect(ctx context.Context) (bool, error) {
	// Build TLS config.
	tlsConfig := &tls.Config{
		// #nosec G402 -- TLS_SKIP_VERIFY is an explicit test-only escape hatch documented as unsafe.
		InsecureSkipVerify: c.cfg.TLSSkipVerify,
	}

	if c.cfg.CACert != "" {
		caCert, err := os.ReadFile(c.cfg.CACert)
		if err != nil {
			return false, fmt.Errorf("reading CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return false, fmt.Errorf("failed to parse CA cert")
		}
		tlsConfig.RootCAs = pool
	}

	// Build WebSocket URL.
	wsURL := c.cfg.DrydockURL + "/api/portwing/ws"
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	wsURL = strings.Replace(wsURL, "http://", "ws://", 1)

	dialer := websocket.Dialer{
		TLSClientConfig:  tlsConfig,
		HandshakeTimeout: 10 * time.Second,
	}

	slog.Info("connecting to controller", "url", wsURL)

	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return false, fmt.Errorf("websocket dial: %w", err)
	}
	conn.SetReadLimit(maxReadSize)

	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()

	// Send hello.
	if err := c.sendHello(ctx); err != nil {
		closeWebSocket(conn, "send hello failure")
		return false, fmt.Errorf("sending hello: %w", err)
	}

	// Wait for welcome.
	welcomeTimeout := time.Duration(c.cfg.WelcomeTimeout) * time.Second
	if err := conn.SetReadDeadline(time.Now().Add(welcomeTimeout)); err != nil {
		closeWebSocket(conn, "set welcome deadline failure")
		return false, fmt.Errorf("setting welcome deadline: %w", err)
	}

	_, msg, err := conn.ReadMessage()
	if err != nil {
		closeWebSocket(conn, "read welcome failure")
		return false, fmt.Errorf("reading welcome: %w", err)
	}

	var env protocol.Envelope
	if err := json.Unmarshal(msg, &env); err != nil {
		closeWebSocket(conn, "parse welcome failure")
		return false, fmt.Errorf("parsing welcome envelope: %w", err)
	}
	if env.Type != protocol.TypeWelcome {
		closeWebSocket(conn, "unexpected welcome type")
		return false, fmt.Errorf("expected welcome, got %q", env.Type)
	}

	// Switch from the one-shot welcome deadline to the steady-state read
	// deadline (SPEC §13.2). readPump re-arms it after every message; if the
	// controller stops answering pings the read times out and we reconnect.
	if err := conn.SetReadDeadline(time.Now().Add(readDeadline(c.cfg.HeartbeatInterval))); err != nil {
		closeWebSocket(conn, "set read deadline failure")
		return false, fmt.Errorf("setting read deadline: %w", err)
	}

	slog.Info("connected to controller")

	// Let adapter handle initial sync (container sync, component sync, etc.).
	sender := &edgeMessageSender{client: c}
	if err := c.adapter.OnConnect(ctx, sender); err != nil {
		slog.Warn("adapter OnConnect failed", "error", err)
	}

	// Send initial metrics.
	c.sendMetrics()

	// Run pumps.
	pumpCtx, pumpCancel := context.WithCancel(ctx)
	defer pumpCancel()

	var wg sync.WaitGroup
	wg.Add(2)

	var readErr error
	go func() {
		defer wg.Done()
		readErr = c.readPump(pumpCtx)
		pumpCancel()
	}()

	go func() {
		defer wg.Done()
		c.writePump(pumpCtx)
	}()

	wg.Wait()

	// Close connection.
	c.connMu.Lock()
	if c.conn != nil {
		closeWebSocket(c.conn, "connection loop end")
		c.conn = nil
	}
	c.connMu.Unlock()

	// Reaching here means the welcome handshake succeeded, so the connection
	// counts as established even if the read pump later returned an error.
	return true, readErr
}

// sendHello sends the hello handshake message. When PRIVATE_KEY_FILE is
// configured, the hello is signed with Ed25519 and the signature fields are
// populated (tokenHash is left empty). Otherwise, tokenHash is set as before.
func (c *Client) sendHello(ctx context.Context) error {
	dockerVersion, err := c.dockerClient.GetVersion(ctx)
	if err != nil {
		dockerVersion = "unknown"
	}

	hostname, _ := os.Hostname()

	capabilities := []string{
		"compose",
		"exec",
		"metrics",
		"events",
	}
	capabilities = append(capabilities, c.adapter.Capabilities()...)

	hello := protocol.HelloMessage{
		Version:       protocol.AgentVersion,
		Protocol:      protocol.ProtocolString,
		AgentID:       c.cfg.AgentID,
		AgentName:     c.cfg.AgentName,
		DockerVersion: dockerVersion,
		Hostname:      hostname,
		Capabilities:  capabilities,
	}

	// Attempt Ed25519 signing if a private key is configured.
	if c.cfg.PrivateKeyFile != "" {
		if err := c.signHello(ctx, &hello); err != nil {
			slog.Warn("ed25519 hello signing failed, falling back to token hash", "error", err)
			c.setTokenHash(&hello)
		}
	} else {
		c.setTokenHash(&hello)
	}

	// Merge adapter-specific hello extension fields.
	if ext := c.adapter.HelloExtension(); ext != nil {
		hello.DrydockCompat = ext.DrydockCompat
		hello.WatcherTypes = ext.WatcherTypes
		hello.TriggerTypes = ext.TriggerTypes
	}

	return c.sendTypedMessage(protocol.TypeHello, hello)
}

// setTokenHash sets the TokenHash field from cfg.Token.
func (c *Client) setTokenHash(hello *protocol.HelloMessage) {
	if c.cfg.Token != "" {
		hash := sha256.Sum256([]byte(c.cfg.Token))
		hello.TokenHash = fmt.Sprintf("%x", hash)
	}
}

// signHello signs the hello message with the configured Ed25519 private key.
// The WebSocket upgrade path is used as the "path" in the canonical string,
// with the empty-body hash.
func (c *Client) signHello(_ context.Context, hello *protocol.HelloMessage) error {
	priv, err := auth.LoadPrivateKey(c.cfg.PrivateKeyFile)
	if err != nil {
		return fmt.Errorf("loading private key: %w", err)
	}

	pub := priv.Public().(ed25519.PublicKey)
	keyID := auth.KeyIDForPublicKey(pub)

	nonce, err := auth.NewNonce()
	if err != nil {
		return fmt.Errorf("generating nonce: %w", err)
	}

	tsUnix := time.Now().Unix()

	// The canonical path is the WebSocket upgrade URL path.
	wsPath := "/api/portwing/ws"
	msg := auth.CanonicalMessage("GET", wsPath, auth.BodyHashHex(nil), tsUnix, nonce)
	sig := ed25519.Sign(priv, msg)

	hello.PubKeyID = keyID
	hello.Timestamp = tsUnix
	hello.Nonce = nonce
	hello.Signature = base64.RawURLEncoding.EncodeToString(sig)
	// Do not set TokenHash when using Ed25519 auth.
	hello.TokenHash = ""

	return nil
}

// readPump reads messages from the WebSocket and dispatches them.
func (c *Client) readPump(ctx context.Context) error {
	sender := &edgeMessageSender{client: c}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read message: %w", err)
		}

		// Re-arm the read deadline on every received message, including pings
		// (SPEC §13.2). A read that blocks past the deadline means the
		// controller has gone silent and the connection is dead.
		if err := c.conn.SetReadDeadline(time.Now().Add(readDeadline(c.cfg.HeartbeatInterval))); err != nil {
			return fmt.Errorf("resetting read deadline: %w", err)
		}

		var env protocol.Envelope
		if err := json.Unmarshal(msg, &env); err != nil {
			slog.Warn("invalid message envelope", "error", err)
			continue
		}

		switch env.Type {
		case protocol.TypeRequest:
			var req protocol.RequestMessage
			if err := json.Unmarshal(env.Data, &req); err != nil {
				slog.Warn("invalid request message", "error", err)
				continue
			}
			// Bound concurrent request handlers (maxStreams). Reject rather than
			// block the read loop, which must keep servicing pings and exec I/O.
			select {
			case c.streamSem <- struct{}{}:
				go func() {
					defer func() { <-c.streamSem }()
					c.handleRequest(ctx, req)
				}()
			default:
				slog.Warn("concurrent request limit reached, rejecting", "max", maxStreams, "request_id", req.RequestID)
				_ = c.sendTypedMessage(protocol.TypeError, protocol.ErrorMessage{
					Message:   "agent busy: too many concurrent requests",
					RequestID: req.RequestID,
				})
			}

		case protocol.TypeExecStart:
			var msg protocol.ExecStartMessage
			if err := json.Unmarshal(env.Data, &msg); err != nil {
				slog.Warn("invalid exec_start message", "error", err)
				continue
			}
			c.auditor.ExecStart(c.cfg.DrydockURL, msg.ContainerID, msg.ExecID)
			go c.StartExec(ctx, msg)

		case protocol.TypeExecInput:
			var msg protocol.ExecInputMessage
			if err := json.Unmarshal(env.Data, &msg); err != nil {
				slog.Warn("invalid exec_input message", "error", err)
				continue
			}
			c.HandleInput(msg)

		case protocol.TypeExecResize:
			var msg protocol.ExecResizeMessage
			if err := json.Unmarshal(env.Data, &msg); err != nil {
				slog.Warn("invalid exec_resize message", "error", err)
				continue
			}
			c.HandleResize(ctx, msg)

		case protocol.TypeExecEnd:
			var msg protocol.ExecEndMessage
			if err := json.Unmarshal(env.Data, &msg); err != nil {
				slog.Warn("invalid exec_end message", "error", err)
				continue
			}
			c.EndExec(msg)

		case protocol.TypePing:
			var ping protocol.PingMessage
			if err := json.Unmarshal(env.Data, &ping); err != nil {
				slog.Debug("invalid ping message", "error", err)
				continue
			}
			// Best-effort pong reply; connection loss will surface on next read.
			_ = c.sendTypedMessage(protocol.TypePong, protocol.PongMessage(ping))

		default:
			// Delegate to adapter for unrecognized message types.
			if !c.adapter.HandleMessage(ctx, sender, env.Type, env.Data) {
				slog.Debug("unhandled message type", "type", env.Type)
			}
		}
	}
}

// handleRequest executes a Docker API request locally and sends the response
// back over the WebSocket.
func (c *Client) handleRequest(ctx context.Context, req protocol.RequestMessage) {
	start := time.Now()
	isStream := docker.IsStreamingPath(req.Path)

	var bodyReader io.Reader
	if req.Body != nil {
		bodyReader = strings.NewReader(string(req.Body))
	}

	var resp *http.Response
	var err error

	if isStream {
		resp, err = c.dockerClient.DoStream(ctx, req.Method, req.Path, bodyReader)
	} else {
		resp, err = c.dockerClient.Do(ctx, req.Method, req.Path, bodyReader)
	}

	if err != nil {
		c.auditor.APIRequest(c.cfg.DrydockURL, req.Method, req.Path, audit.OutcomeError, 0, msEdge(start))
		// Best-effort error reply; connection loss will surface on the read pump.
		_ = c.sendTypedMessage(protocol.TypeError, protocol.ErrorMessage{
			Message:   err.Error(),
			RequestID: req.RequestID,
		})
		return
	}
	defer resp.Body.Close()
	c.auditor.APIRequest(c.cfg.DrydockURL, req.Method, req.Path, audit.OutcomeAllowed, resp.StatusCode, msEdge(start))

	// Build response headers.
	headers := make(map[string]string)
	for key := range resp.Header {
		headers[key] = resp.Header.Get(key)
	}

	if isStream {
		// Send initial response header; best-effort — connection loss surfaces on the read pump.
		_ = c.sendTypedMessage(protocol.TypeResponse, protocol.ResponseMessage{
			RequestID:   req.RequestID,
			StatusCode:  resp.StatusCode,
			Headers:     headers,
			IsStream:    true,
			ContentType: resp.Header.Get("Content-Type"),
		})

		// Stream body in chunks.
		buf := make([]byte, 32*1024)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				encoded := base64.StdEncoding.EncodeToString(buf[:n])
				_ = c.sendTypedMessage(protocol.TypeStream, protocol.StreamMessage{
					RequestID: req.RequestID,
					Data:      encoded,
				})
			}
			if readErr != nil {
				break
			}
		}

		_ = c.sendTypedMessage(protocol.TypeStreamEnd, protocol.StreamEndMessage{
			RequestID: req.RequestID,
			Reason:    "complete",
		})
	} else {
		// Read body (capped).
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))

		_ = c.sendTypedMessage(protocol.TypeResponse, protocol.ResponseMessage{
			RequestID:   req.RequestID,
			StatusCode:  resp.StatusCode,
			Headers:     headers,
			Body:        json.RawMessage(body),
			ContentType: resp.Header.Get("Content-Type"),
		})
	}
}

// writePump handles periodic outgoing messages: metrics, container refreshes,
// and keepalive pings.
func (c *Client) writePump(ctx context.Context) {
	heartbeat := time.Duration(c.cfg.HeartbeatInterval) * time.Second

	pollInterval := c.adapter.PollInterval()
	if pollInterval <= 0 {
		pollInterval = c.cfg.DDPollInterval
	}
	pollDuration := time.Duration(pollInterval) * time.Second

	heartbeatTicker := time.NewTicker(heartbeat)
	defer heartbeatTicker.Stop()

	pollTicker := time.NewTicker(pollDuration)
	defer pollTicker.Stop()

	sender := &edgeMessageSender{client: c}

	for {
		select {
		case <-ctx.Done():
			return

		case <-heartbeatTicker.C:
			// Send metrics.
			c.sendMetrics()

			// Send keepalive ping; best-effort — connection loss surfaces on the read pump.
			_ = c.sendTypedMessage(protocol.TypePing, protocol.PingMessage{
				Timestamp: time.Now().UnixMilli(),
			})

		case <-pollTicker.C:
			// Refresh container inventory via adapter.
			added, updated, removed, err := c.adapter.RefreshContainers(ctx)
			if err != nil {
				slog.Warn("container refresh failed", "error", err)
				continue
			}
			if err := c.adapter.OnContainerRefresh(ctx, sender, added, updated, removed); err != nil {
				slog.Warn("container refresh notify failed", "error", err)
			}
		}
	}
}

// sendMetrics collects and sends host metrics.
func (c *Client) sendMetrics() {
	m, err := c.collector.Collect()
	if err != nil {
		slog.Debug("metrics collection failed", "error", err)
		return
	}
	// Best-effort metrics send; connection loss surfaces on the read pump.
	_ = c.sendTypedMessage(protocol.TypeMetrics, m)
}

// sendTypedMessage wraps data in an Envelope and sends it over the WebSocket.
func (c *Client) sendTypedMessage(msgType string, data interface{}) error {
	rawData, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshaling %s: %w", msgType, err)
	}

	env := protocol.Envelope{
		Type: msgType,
		Data: json.RawMessage(rawData),
	}

	c.sendMessage(env)
	return nil
}

// sendMessage performs a thread-safe WebSocket write.
func (c *Client) sendMessage(env protocol.Envelope) {
	c.connMu.Lock()
	defer c.connMu.Unlock()

	if c.conn == nil {
		return
	}

	if err := c.conn.WriteJSON(env); err != nil {
		slog.Warn("websocket write failed", "type", env.Type, "error", err)
	}
}

// msEdge returns elapsed milliseconds since start as a float64.
func msEdge(start time.Time) float64 {
	return float64(time.Since(start).Nanoseconds()) / 1e6
}

// readDeadline returns the steady-state WebSocket read deadline:
// max(2 * HEARTBEAT_INTERVAL, 60s). Exceeding it means pings have gone
// unanswered, so the connection is treated as dead (SPEC §13.2).
func readDeadline(heartbeatSeconds int) time.Duration {
	d := 2 * time.Duration(heartbeatSeconds) * time.Second
	if d < 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

func jitteredDuration(delay time.Duration) time.Duration {
	const (
		minMillis = 750
		span      = 500
	)
	n, err := crand.Int(crand.Reader, big.NewInt(span+1))
	if err != nil {
		slog.Warn("generating reconnect jitter", "error", err)
		return delay
	}
	factorMillis := minMillis + n.Int64()
	return time.Duration((int64(delay) * factorMillis) / 1000)
}

func closeWebSocket(conn *websocket.Conn, context string) {
	if err := conn.Close(); err != nil {
		slog.Debug("closing websocket", "context", context, "error", err)
	}
}

// startHealthServer starts a minimal HTTP server for Docker HEALTHCHECK.
func (c *Client) startHealthServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_portwing/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "healthy",
		})
	})
	c.healthServer = &http.Server{
		Addr:              c.cfg.BindAddress + ":" + c.cfg.Port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := c.healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Warn("health server error", "error", err)
		}
	}()
}
