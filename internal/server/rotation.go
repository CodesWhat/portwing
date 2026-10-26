// SPDX-License-Identifier: MIT

package server

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/auth"
	"github.com/codeswhat/portwing/internal/protocol"
)

type verifiedKeyIDContextKey struct{}

const rotationRoute = protocol.KeyRotationPath

func (s *Server) handleKeyRotation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.cfg.KeyRotationDir == "" || s.ed25519.Registry == nil {
		http.NotFound(w, r)
		return
	}
	caller, _ := r.Context().Value(verifiedKeyIDContextKey{}).(string)
	if caller == "" {
		http.Error(w, "Ed25519 authentication required", http.StatusForbidden)
		return
	}
	if r.Header.Get(auth.HeaderSignatureVersion) != auth.SignatureVersion2 || r.URL.RawQuery != "" || r.URL.ForceQuery {
		http.Error(w, "rotation requires signature version 2 and no query parameters", http.StatusBadRequest)
		return
	}
	action := strings.TrimPrefix(r.URL.Path, rotationRoute)
	method := http.MethodPost
	if action == "" {
		method = http.MethodGet
	} else if action != "/prepare" && action != "/commit" && action != "/abort" {
		http.NotFound(w, r)
		return
	}
	if r.Method != method {
		w.Header().Set("Allow", method)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var status auth.RotationStatus
	var err error
	if action == "" {
		status, err = s.ed25519.Registry.RotationStatus(caller)
	} else {
		var body struct {
			PublicKey string `json:"public_key,omitempty"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "invalid rotation request", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			http.Error(w, "invalid rotation request", http.StatusBadRequest)
			return
		}
		switch action {
		case "/prepare":
			var pub []byte
			pub, err = base64.StdEncoding.Strict().DecodeString(body.PublicKey)
			if err != nil || len(pub) != ed25519.PublicKeySize {
				err = auth.ErrRotationInvalid
			} else {
				status, err = s.ed25519.Registry.PrepareRotation(caller, pub)
			}
		case "/commit", "/abort":
			if body.PublicKey != "" {
				err = auth.ErrRotationInvalid
			} else if action == "/commit" {
				status, err = s.ed25519.Registry.CommitRotation(caller)
			} else {
				status, err = s.ed25519.Registry.AbortRotation(caller)
			}
		}
		s.auditor.KeyRotation(s.rateLimiter.clientIP(r), strings.TrimPrefix(action, "/"), caller, rotationAuditOutcome(err))
	}
	if err != nil {
		code := http.StatusInternalServerError
		switch {
		case errors.Is(err, auth.ErrRotationDisabled):
			code = http.StatusNotFound
		case errors.Is(err, auth.ErrRotationForbidden):
			code = http.StatusForbidden
		case errors.Is(err, auth.ErrRotationConflict):
			code = http.StatusConflict
		case errors.Is(err, auth.ErrRotationInvalid):
			code = http.StatusBadRequest
		default:
			slog.Error("persisting key rotation failed", "error", err)
		}
		http.Error(w, http.StatusText(code), code)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func rotationAuditOutcome(err error) string {
	if err == nil {
		return audit.OutcomeAllowed
	}
	for _, rejected := range []error{auth.ErrRotationForbidden, auth.ErrRotationConflict, auth.ErrRotationInvalid, auth.ErrRotationDisabled} {
		if errors.Is(err, rejected) {
			return audit.OutcomeDenied
		}
	}
	return audit.OutcomeError
}
