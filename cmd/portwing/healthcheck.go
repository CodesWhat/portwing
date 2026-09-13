package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
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
	verification, err := healthcheckCertificateVerification(os.Getenv("TLS_CERT"))
	if err != nil {
		return err
	}
	transport := &http.Transport{
		TLSClientConfig:        verification,
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

// healthcheckCertificateVerification pins the server's configured leaf instead
// of requiring a public CA or a localhost SAN for the local health probe.
func healthcheckCertificateVerification(path string) (*tls.Config, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read TLS_CERT: %w", err)
	}
	var block *pem.Block
	for {
		block, data = pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("TLS_CERT contains no PEM certificate")
		}
		if block.Type == "CERTIFICATE" {
			break
		}
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse TLS_CERT: %w", err)
	}
	return &tls.Config{
		// Replace CA/hostname verification with an exact leaf pin. The TLS
		// handshake still proves possession of the corresponding private key.
		InsecureSkipVerify: true, // #nosec G402 -- VerifyConnection enforces the configured certificate pin
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 || !bytes.Equal(state.PeerCertificates[0].Raw, certificate.Raw) {
				return fmt.Errorf("health endpoint certificate does not match TLS_CERT")
			}
			return nil
		},
	}, nil
}
