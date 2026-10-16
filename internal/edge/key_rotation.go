// SPDX-License-Identifier: MIT

package edge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/auth"
	"github.com/codeswhat/portwing/internal/protocol"
)

func (c *Client) initKeyRotation() error {
	c.keyRotationOnce.Do(func() {
		if c.cfg.KeyRotationDir != "" {
			c.keyRotation, c.keyRotationErr = auth.NewEdgeKeyRotation(c.cfg.PrivateKeyFile, c.cfg.KeyRotationDir)
		}
	})
	return c.keyRotationErr
}

func (c *Client) handleKeyRotationRequest(ctx context.Context, req protocol.RequestMessage, target outboundTarget) bool {
	if !strings.HasPrefix(req.Path, protocol.KeyRotationPath) {
		return false
	}
	start := time.Now()
	code := http.StatusInternalServerError
	reply := func(status int, data any) {
		code = status
		body, _ := json.Marshal(data)
		response := protocol.ResponseMessage{
			RequestID: req.RequestID, StatusCode: status, Body: body, ContentType: "application/json",
			Headers: map[string]string{"Cache-Control": "no-store"},
		}
		if c.hasControllerCap(protocol.CapResponseBodyBase64) {
			response.Body = nil
			response.BodyBase64 = base64.StdEncoding.EncodeToString(body)
		}
		_ = c.sendTypedMessageTo(target, protocol.TypeResponse, response)
	}
	fail := func(status int) { reply(status, map[string]string{"error": http.StatusText(status)}) }
	defer func() {
		if c.auditor != nil {
			outcome := audit.OutcomeAllowed
			if code >= 400 {
				outcome = audit.OutcomeDenied
			}
			c.auditor.APIRequest(c.cfg.DrydockURL, req.Method, req.Path, outcome, code, float64(time.Since(start).Microseconds())/1000)
		}
	}()
	if c.cfg.KeyRotationDir == "" {
		fail(http.StatusNotFound)
		return true
	}
	if err := c.initKeyRotation(); err != nil {
		fail(http.StatusInternalServerError)
		return true
	}
	u, err := url.ParseRequestURI(req.Path)
	if err != nil || u.RawQuery != "" || u.ForceQuery || len(req.Body) > 4096 {
		fail(http.StatusBadRequest)
		return true
	}
	const edgePath = protocol.KeyRotationPath + "/edge"
	action := strings.TrimPrefix(u.Path, edgePath)
	if !strings.HasPrefix(u.Path, edgePath) || (action != "" && action != "/prepare" && action != "/activate" && action != "/abort") {
		fail(http.StatusNotFound)
		return true
	}
	method := http.MethodPost
	if action == "" {
		method = http.MethodGet
	}
	if req.Method != method {
		fail(http.StatusMethodNotAllowed)
		return true
	}
	var body struct {
		KeyID string `json:"key_id,omitempty"`
	}
	if action != "" {
		decoder := json.NewDecoder(bytes.NewReader(req.Body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			fail(http.StatusBadRequest)
			return true
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			fail(http.StatusBadRequest)
			return true
		}
		if action != "/activate" && body.KeyID != "" {
			fail(http.StatusBadRequest)
			return true
		}
	}
	c.connMu.Lock()
	if ctx.Err() != nil || c.conn != target.conn || c.sendCh != target.ch {
		c.connMu.Unlock()
		fail(http.StatusConflict)
		return true
	}
	caller := c.signedHelloKeyID
	status := c.keyRotation.Status()
	if action != "" && caller != status.CurrentKeyID {
		c.connMu.Unlock()
		fail(http.StatusConflict)
		return true
	}
	switch action {
	case "/prepare":
		status, err = c.keyRotation.Prepare()
	case "/activate":
		status, err = c.keyRotation.Activate(body.KeyID)
	case "/abort":
		status, err = c.keyRotation.Abort()
	}
	c.connMu.Unlock()
	if action != "" && c.auditor != nil {
		outcome := audit.OutcomeAllowed
		if err != nil {
			outcome = audit.OutcomeError
		}
		c.auditor.KeyRotation(c.cfg.DrydockURL, strings.TrimPrefix(action, "/"), caller, outcome)
	}
	if errors.Is(err, auth.ErrRotationInvalid) {
		fail(http.StatusBadRequest)
	} else if err != nil {
		slog.Error("persisting edge key rotation failed", "error", err)
		fail(http.StatusInternalServerError)
	} else {
		reply(http.StatusOK, status)
	}
	return true
}
