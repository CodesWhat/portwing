package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"
)

func healthcheckURL() (string, error) {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil || number == 0 {
		return "", fmt.Errorf("invalid PORT %q", port)
	}
	scheme := "http"
	if os.Getenv("TLS_CERT") != "" {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort("localhost", port) + "/health", nil
}

func runHealthcheck() error {
	endpoint, err := healthcheckURL()
	if err != nil {
		return err
	}
	transport := &http.Transport{
		// The probe only connects to localhost and never follows redirects. Like
		// the container's previous wget probe, it accepts a self-signed local cert.
		TLSClientConfig:        &tls.Config{InsecureSkipVerify: true}, // #nosec G402 -- loopback-only health probe
		DisableKeepAlives:      true,
		MaxResponseHeaderBytes: 64 << 10,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		Timeout:       4 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("probe failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy HTTP status %d", resp.StatusCode)
	}
	return nil
}
