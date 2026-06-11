package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/codeswhat/lookout/internal/adapter"
	"github.com/codeswhat/lookout/internal/audit"
	"github.com/codeswhat/lookout/internal/config"
	"github.com/codeswhat/lookout/internal/docker"
	"github.com/codeswhat/lookout/internal/mcp"
	"github.com/codeswhat/lookout/internal/metrics"
	"github.com/codeswhat/lookout/internal/protocol"
)

// hopByHopHeaders are headers that must not be forwarded by proxies.
var hopByHopHeaders = map[string]bool{
	"Connection":          true,
	"Keep-Alive":          true,
	"Transfer-Encoding":   true,
	"Te":                  true,
	"Trailer":             true,
	"Upgrade":             true,
	"Proxy-Authorization": true,
	"Proxy-Authenticate":  true,
}

// Server is the standard-mode HTTP server that exposes Docker API proxy
// endpoints, adapter-specific routes, and health checks.
type Server struct {
	cfg          *config.Config
	dockerClient *docker.Client
	adapter      adapter.ServerAdapter
	compose      *docker.ComposeManager
	collector    *metrics.Collector
	rateLimiter  *RateLimiter
	verifier     tokenVerifier
	auditor      *audit.Logger
	httpServer   *http.Server
	startTime    time.Time
}

// NewServer creates and configures a new standard-mode Server.
// It returns an error if the TokenHash is set but cannot be parsed; the PHC
// string is validated at startup so malformed configuration is caught early.
func NewServer(cfg *config.Config, dockerClient *docker.Client, a adapter.ServerAdapter) (*Server, error) {
	var verifier tokenVerifier
	switch {
	case cfg.Token != "":
		verifier = &rawTokenVerifier{token: cfg.Token}
	case cfg.TokenHash != "":
		params, err := ParsePHC(cfg.TokenHash)
		if err != nil {
			return nil, fmt.Errorf("parsing TOKEN_HASH: %w", err)
		}
		verifier = newArgon2Verifier(params)
	}
	// verifier == nil means no auth configured.

	auditor, auditClose, err := audit.New(cfg.AuditLog)
	if err != nil {
		return nil, fmt.Errorf("opening audit log: %w", err)
	}
	_ = auditClose // closed when process exits; file is append-only

	s := &Server{
		cfg:          cfg,
		dockerClient: dockerClient,
		adapter:      a,
		compose:      docker.NewComposeManager(cfg.StacksDir, dockerClient.GetAPIVersion(), cfg.DockerSocket),
		collector:    metrics.NewCollector("/var/lib/docker", cfg.SkipDFCollection),
		rateLimiter:  NewRateLimiter(),
		verifier:     verifier,
		auditor:      auditor,
		startTime:    time.Now(),
	}

	if len(cfg.TrustedProxies) > 0 {
		nets, err := ParseTrustedProxies(cfg.TrustedProxies)
		if err != nil {
			return nil, fmt.Errorf("parsing TRUSTED_PROXIES: %w", err)
		}
		s.rateLimiter.SetTrustedProxies(nets)
	}

	mux := http.NewServeMux()
	s.registerRoutes(mux)

	handler := RecoveryMiddleware(http.Handler(mux))

	s.httpServer = &http.Server{
		Addr:    cfg.BindAddress + ":" + cfg.Port,
		Handler: handler,
	}

	// Configure TLS if certs provided.
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		s.httpServer.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			CurvePreferences: []tls.CurveID{
				tls.X25519,
				tls.CurveP256,
			},
			CipherSuites: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
				tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			},
		}
	}

	return s, nil
}

// registerRoutes wires up all HTTP endpoints. Routes requiring authentication
// are wrapped with the auth middleware.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// No auth required.
	mux.HandleFunc("GET /_lookout/health", s.handleHealth)
	mux.HandleFunc("GET /health", s.handleSimpleHealth)

	// Auth required - wrap with audit-aware auth middleware.
	auth := func(h http.HandlerFunc) http.Handler {
		return s.rateLimiter.AuthMiddleware(s.verifier, s.auditor, http.HandlerFunc(h))
	}

	mux.Handle("GET /_lookout/info", auth(s.handleInfo))
	mux.Handle("POST /_lookout/compose", auth(s.handleCompose))
	mux.Handle("GET /_lookout/metrics", auth(s.handleMetrics))
	mux.Handle("GET /metrics", auth(s.handleMetrics))
	mux.Handle("/_lookout/mcp", auth(func(w http.ResponseWriter, r *http.Request) {
		mcp.NewHandler(s.dockerClient, s.collector).ServeHTTP(w, r)
	}))

	// Adapter-specific routes.
	s.adapter.RegisterRoutes(mux, auth)

	// Docker API proxy - catch-all (must be last).
	mux.Handle("/", auth(s.handleDockerProxy))
}

// handleHealth returns the agent health status including Docker connectivity.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	err := s.dockerClient.Ping(ctx)

	status := "healthy"
	dockerStatus := "connected"
	httpStatus := http.StatusOK
	if err != nil {
		status = "unhealthy"
		dockerStatus = "disconnected"
		httpStatus = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": status,
		"docker": dockerStatus,
	})
}

// handleSimpleHealth returns a minimal 200 OK response.
func (s *Server) handleSimpleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

// handleInfo returns agent metadata.
func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	dockerVersion, err := s.dockerClient.GetVersion(ctx)
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
	capabilities = append(capabilities, s.adapter.Capabilities()...)

	info := map[string]interface{}{
		"version":       protocol.AgentVersion,
		"dockerVersion": dockerVersion,
		"mode":          "standard",
		"uptime":        time.Since(s.startTime).String(),
		"hostname":      hostname,
		"agentId":       s.cfg.AgentID,
		"agentName":     s.cfg.AgentName,
		"capabilities":  capabilities,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}

// handleCompose dispatches Docker Compose operations.
func (s *Server) handleCompose(w http.ResponseWriter, r *http.Request) {
	actor := s.rateLimiter.clientIP(r)

	var req docker.ComposeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	resp, err := s.compose.Execute(r.Context(), req)
	if err != nil {
		s.auditor.ComposeOp(actor, req.Operation, req.StackName, audit.OutcomeError)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	outcome := audit.OutcomeAllowed
	if !resp.Success {
		outcome = audit.OutcomeError
	}
	s.auditor.ComposeOp(actor, req.Operation, req.StackName, outcome)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleDockerProxy is the transparent Docker API proxy. It forwards requests
// to the local Docker daemon, handling both regular and streaming responses.
func (s *Server) handleDockerProxy(w http.ResponseWriter, r *http.Request) {
	// Determine if this is a streaming endpoint.
	isStream := docker.IsStreamingPath(r.URL.Path)

	// Check for exec hijack (WebSocket upgrade on /exec/*/start).
	isExecStart := strings.Contains(r.URL.Path, "/exec/") && strings.HasSuffix(r.URL.Path, "/start")
	if isExecStart && isWebSocketUpgrade(r) {
		s.handleExecHijack(w, r)
		return
	}

	// Build Docker API request.
	dockerURL := fmt.Sprintf("http://localhost%s", r.URL.RequestURI())
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, dockerURL, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Copy headers (strip hop-by-hop).
	copyHeaders(proxyReq.Header, r.Header)

	var resp *http.Response
	if isStream {
		resp, err = s.dockerClient.DoStreamRaw(proxyReq)
	} else {
		resp, err = s.dockerClient.DoRaw(proxyReq)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers.
	copyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	// Stream or copy body.
	if isStream {
		s.streamResponse(w, resp.Body)
	} else {
		// io.Copy to a ResponseWriter: errors indicate a dropped client connection.
		_, _ = io.Copy(w, resp.Body)
	}
}

// handleExecHijack handles WebSocket-upgraded exec connections by hijacking
// the HTTP connection and proxying bidirectionally to the Docker daemon.
func (s *Server) handleExecHijack(w http.ResponseWriter, r *http.Request) {
	actor := s.rateLimiter.clientIP(r)
	// Extract exec resource ID from the path: /exec/<id>/start
	execID := ""
	if parts := strings.Split(r.URL.Path, "/"); len(parts) >= 3 {
		execID = parts[len(parts)-2]
	}
	s.auditor.ExecStart(actor, r.URL.Path, execID)

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, clientBuf, err := hj.Hijack()
	if err != nil {
		http.Error(w, fmt.Sprintf("hijack failed: %v", err), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// Connect to Docker daemon.
	dockerConn, err := net.Dial("unix", s.dockerClient.GetSocketPath())
	if err != nil {
		// Best-effort 502 write; client may have already gone.
		_, _ = clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}
	defer dockerConn.Close()

	// Forward the original request to Docker.
	rawReq := fmt.Sprintf(
		"%s %s HTTP/1.1\r\nHost: localhost\r\nConnection: Upgrade\r\nUpgrade: tcp\r\nContent-Type: application/json\r\n",
		r.Method, r.URL.RequestURI(),
	)
	body, _ := io.ReadAll(r.Body)
	rawReq += fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), string(body))

	if _, err := dockerConn.Write([]byte(rawReq)); err != nil {
		_, _ = clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}

	// Read Docker response.
	dockerBuf := bufio.NewReader(dockerConn)
	resp, err := http.ReadResponse(dockerBuf, nil)
	if err != nil {
		_, _ = clientConn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\n\r\n"))
		return
	}

	// Forward the response status to the client.
	_ = resp.Write(clientConn)

	if resp.StatusCode != http.StatusSwitchingProtocols {
		return
	}

	// Bidirectional proxy; io.Copy errors just mean one side closed.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(dockerConn, clientBuf)
	}()

	go func() {
		defer wg.Done()
		_, _ = io.Copy(clientConn, dockerBuf)
	}()

	wg.Wait()
}

// isWebSocketUpgrade checks if the request is a WebSocket upgrade request.
func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") ||
		strings.EqualFold(r.Header.Get("Upgrade"), "tcp") ||
		strings.EqualFold(r.Header.Get("Connection"), "Upgrade")
}

// copyHeaders copies headers from src to dst, stripping hop-by-hop headers.
func copyHeaders(dst, src http.Header) {
	for key, values := range src {
		if hopByHopHeaders[http.CanonicalHeaderKey(key)] {
			continue
		}
		for _, v := range values {
			dst.Add(key, v)
		}
	}
}

// streamResponse copies from body to the ResponseWriter, flushing after each
// read for streaming endpoints.
func (s *Server) streamResponse(w http.ResponseWriter, body io.Reader) {
	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 32*1024)

	for {
		n, err := body.Read(buf)
		if n > 0 {
			// Write to ResponseWriter: errors indicate a dropped client connection.
			_, _ = w.Write(buf[:n])
			if canFlush {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// ListenAndServe starts the HTTP server. It launches background container
// polling and uses TLS if certificates are configured.
func (s *Server) ListenAndServe() error {
	go s.pollContainers()

	if s.cfg.TLSCert != "" && s.cfg.TLSKey != "" {
		return s.httpServer.ListenAndServeTLS(s.cfg.TLSCert, s.cfg.TLSKey)
	}
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// pollContainers periodically refreshes the container inventory via the
// adapter and lets the adapter broadcast changes.
func (s *Server) pollContainers() {
	ctx := context.Background()

	// Initial refresh (builds inventory).
	if _, _, _, err := s.adapter.RefreshContainers(ctx); err != nil {
		slog.Error("initial container inventory failed", "error", err)
	}

	interval := s.adapter.PollInterval()
	if interval <= 0 {
		interval = s.cfg.DDPollInterval
	}

	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		added, updated, removed, err := s.adapter.RefreshContainers(ctx)
		if err != nil {
			slog.Error("container refresh failed", "error", err)
			continue
		}
		// In standard mode, sender is nil — adapter handles SSE internally.
		if err := s.adapter.OnContainerRefresh(ctx, nil, added, updated, removed); err != nil {
			slog.Error("container refresh notify failed", "error", err)
		}
	}
}
