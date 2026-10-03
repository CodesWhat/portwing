package edge

// Tests for the controller dial's proxy support. http.ProxyFromEnvironment
// caches the environment on first use and never proxies loopback, so the
// dial is exercised through the injectable hook, and the default hook's
// environment behaviour runs in a child process with a fresh environment.

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/protocol"
)

// connectProxy is an httptest CONNECT proxy that records each requested
// target and tunnels it to the real controller address.
type connectProxy struct {
	*httptest.Server
	mu       sync.Mutex
	connects []string
}

func (p *connectProxy) targets() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.connects...)
}

func newConnectProxy(t *testing.T, upstream string) *connectProxy {
	t.Helper()
	p := &connectProxy{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		p.mu.Lock()
		p.connects = append(p.connects, r.Host)
		p.mu.Unlock()

		up, err := net.Dial("tcp", upstream)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			_ = up.Close()
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		client, _, err := hj.Hijack()
		if err != nil {
			_ = up.Close()
			return
		}
		_, _ = client.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
		go func() { _, _ = io.Copy(up, client); _ = up.Close() }()
		go func() { _, _ = io.Copy(client, up); _ = client.Close() }()
	}))
	t.Cleanup(p.Close)
	return p
}

func proxyTestController(t *testing.T) string {
	t.Helper()
	return newControllerServer(t, func(ctrl *websocket.Conn) {
		readAndAckHello(t, ctrl)
		sendWelcomeMsg(t, ctrl, protocol.WelcomeMessage{PollInterval: 7})
		_ = ctrl.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, _, _ = ctrl.ReadMessage()
	})
}

func proxyTestConfig(controllerURL string) *config.Config {
	return &config.Config{
		DrydockURL:        controllerURL,
		HeartbeatInterval: 30,
		WelcomeTimeout:    5,
		ReconnectDelay:    1,
		MaxReconnectDelay: 60,
		DDPollInterval:    300,
		SkipDFCollection:  true,
	}
}

// TestConnectGoesThroughProxy points the controller URL at a hostname that
// only the proxy can reach. The handshake completing proves the dial went
// through the proxy, and the recorded CONNECT proves it asked for that host.
func TestConnectGoesThroughProxy(t *testing.T) {
	t.Parallel()

	controller := proxyTestController(t)
	controllerAddr := strings.TrimPrefix(controller, "http://")
	proxy := newConnectProxy(t, controllerAddr)
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatalf("parse proxy url: %v", err)
	}

	_, port, _ := net.SplitHostPort(controllerAddr)
	const host = "controller.proxy-test.invalid"
	c := newWireClient(t, proxyTestConfig("http://"+net.JoinHostPort(host, port)))
	c.proxy = func(*http.Request) (*url.URL, error) { return proxyURL, nil }

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	established, err := c.connect(ctx)
	if !established {
		t.Fatalf("established = false, want true (err=%v)", err)
	}
	if c.welcomePollInterval != 7 {
		t.Errorf("welcomePollInterval = %d, want 7", c.welcomePollInterval)
	}
	got := proxy.targets()
	want := net.JoinHostPort(host, port)
	if len(got) != 1 || got[0] != want {
		t.Errorf("proxy CONNECT targets = %v, want [%s]", got, want)
	}
}

// TestConnectBypassesProxyWhenHookReturnsNil covers the NO_PROXY outcome: the
// hook declines to proxy, so the dial goes straight to the controller and the
// proxy sees nothing.
func TestConnectBypassesProxyWhenHookReturnsNil(t *testing.T) {
	t.Parallel()

	controller := proxyTestController(t)
	proxy := newConnectProxy(t, strings.TrimPrefix(controller, "http://"))

	c := newWireClient(t, proxyTestConfig(controller))
	var asked sync.Map
	c.proxy = func(r *http.Request) (*url.URL, error) {
		asked.Store(r.URL.Host, true)
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	established, err := c.connect(ctx)
	if !established {
		t.Fatalf("established = false, want true (err=%v)", err)
	}
	if got := proxy.targets(); len(got) != 0 {
		t.Errorf("proxy saw %v, want no connections", got)
	}
	if _, ok := asked.Load(strings.TrimPrefix(controller, "http://")); !ok {
		t.Error("proxy hook was never consulted for the controller host")
	}
}

// TestProxyFuncDefaultsToEnvironment pins the default wiring: with no
// injected hook the dial uses http.ProxyFromEnvironment.
func TestProxyFuncDefaultsToEnvironment(t *testing.T) {
	t.Parallel()

	c := &Client{}
	got := reflect.ValueOf(c.proxyFunc()).Pointer()
	want := reflect.ValueOf(http.ProxyFromEnvironment).Pointer()
	if got != want {
		t.Errorf("default proxyFunc is not http.ProxyFromEnvironment")
	}

	injected := func(*http.Request) (*url.URL, error) { return nil, nil }
	c.proxy = injected
	if reflect.ValueOf(c.proxyFunc()).Pointer() != reflect.ValueOf(injected).Pointer() {
		t.Errorf("injected proxy hook not returned by proxyFunc")
	}
}

const proxyEnvHelper = "PORTWING_PROXY_ENV_HELPER"

// TestProxyFuncEnvironment runs the default hook in a child process whose
// environment is set before first use, so the sync.Once cache inside
// net/http cannot leak between cases or from the parent.
func TestProxyFuncEnvironment(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		scheme string
		env    []string
		want   string
	}{
		{"https proxy", "https", []string{"HTTPS_PROXY=http://proxy.example:3128"}, "http://proxy.example:3128"},
		{"http proxy for plain scheme", "http", []string{"HTTP_PROXY=http://plain.example:8080"}, "http://plain.example:8080"},
		{"proxy credentials in url", "https", []string{"HTTPS_PROXY=http://user:pw@proxy.example:3128"}, "http://user:pw@proxy.example:3128"},
		{"no_proxy exact host", "https", []string{"HTTPS_PROXY=http://proxy.example:3128", "NO_PROXY=controller.corp"}, "direct"},
		{"no_proxy domain suffix", "https", []string{"HTTPS_PROXY=http://proxy.example:3128", "NO_PROXY=.corp"}, "direct"},
		{"no_proxy other host", "https", []string{"HTTPS_PROXY=http://proxy.example:3128", "NO_PROXY=other.corp"}, "http://proxy.example:3128"},
		{"nothing set", "https", nil, "direct"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// #nosec G204 G702 -- re-executes this test binary with a fixed -test.run filter.
			cmd := exec.Command(os.Args[0], "-test.run=^TestProxyEnvHelperProcess$")
			cmd.Env = append(cleanProxyEnv(), tc.env...)
			cmd.Env = append(cmd.Env, proxyEnvHelper+"="+tc.scheme)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("helper process: %v\n%s", err, out)
			}
			if got := proxyHelperResult(string(out)); got != tc.want {
				t.Errorf("proxy for %s://controller.corp = %q, want %q", tc.scheme, got, tc.want)
			}
		})
	}
}

func cleanProxyEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k := strings.ToUpper(strings.SplitN(kv, "=", 2)[0])
		if k == "HTTP_PROXY" || k == "HTTPS_PROXY" || k == "NO_PROXY" || k == "REQUEST_METHOD" {
			continue
		}
		env = append(env, kv)
	}
	return env
}

func proxyHelperResult(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "PROXY="); ok {
			return v
		}
	}
	return "missing"
}

// TestProxyEnvHelperProcess is the child half of TestProxyFuncEnvironment. It
// is a no-op unless the parent set the marker variable.
func TestProxyEnvHelperProcess(t *testing.T) {
	scheme := os.Getenv(proxyEnvHelper)
	if scheme == "" {
		t.Skip("child process helper")
	}
	req, err := http.NewRequest(http.MethodGet, scheme+"://controller.corp/api/portwing/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := (&Client{}).proxyFunc()(req)
	if err != nil {
		t.Fatal(err)
	}
	result := "direct"
	if u != nil {
		result = u.String()
	}
	_, _ = os.Stdout.WriteString("PROXY=" + result + "\n")
}
