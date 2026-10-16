// SPDX-License-Identifier: MIT

package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

type EdgeRotationStatus struct {
	RotationStatus
	PendingPublicKey  string `json:"pending_public_key,omitempty"`
	ActivationPending bool   `json:"activation_pending"`
}

type edgeCredentialState struct {
	Version int    `json:"version"`
	RootID  string `json:"root_key_id"`
	Current []byte `json:"current"`
	Pending []byte `json:"pending,omitempty"`
	Trying  bool   `json:"trying_pending"`
}

type EdgeKeyRotation struct {
	mu      sync.RWMutex
	path    string
	state   edgeCredentialState
	current ed25519.PrivateKey
	pending ed25519.PrivateKey
}

func NewEdgeKeyRotation(bootstrap, dir string) (*EdgeKeyRotation, error) {
	private, err := LoadPrivateKey(bootstrap)
	if err != nil {
		return nil, err
	}
	if err := PrepareRotationDirectory(dir); err != nil {
		return nil, err
	}
	root := KeyIDForPublicKey(private.Public().(ed25519.PublicKey))
	pem, err := MarshalPrivateKeyPEM(private)
	if err != nil {
		return nil, err
	}
	r := &EdgeKeyRotation{path: filepath.Join(dir, "edge-credentials.json"), current: private,
		state: edgeCredentialState{Version: 1, RootID: root, Current: pem}}
	f, err := openCredentialFile(r.path, "edge rotation credentials")
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := readRotationState(io.LimitReader(f, 65537))
	if err != nil {
		return nil, fmt.Errorf("reading edge credentials: %w", err)
	}
	if len(data) > 65536 {
		return nil, fmt.Errorf("edge rotation credentials exceed 64 KiB")
	}
	var state edgeCredentialState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("decoding edge credentials: %w", err)
	}
	r.state = state
	if r.state.Version != 1 || r.state.RootID != root {
		return nil, fmt.Errorf("edge credentials do not match the configured bootstrap key")
	}
	r.current, err = ParsePrivateKeyPEM(r.state.Current)
	if err != nil {
		return nil, fmt.Errorf("invalid current edge credential: %w", err)
	}
	if len(r.state.Pending) != 0 {
		r.pending, err = ParsePrivateKeyPEM(r.state.Pending)
		if err != nil {
			return nil, fmt.Errorf("invalid pending edge credential: %w", err)
		}
		if r.current.Equal(r.pending) {
			return nil, fmt.Errorf("pending edge credential must differ from current")
		}
	} else if r.state.Trying {
		return nil, fmt.Errorf("edge credential trial has no pending key")
	}
	if err := syncRotationStateDirectory(dir); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *EdgeKeyRotation) SigningKey() ed25519.PrivateKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.state.Trying {
		return append(ed25519.PrivateKey(nil), r.pending...)
	}
	return append(ed25519.PrivateKey(nil), r.current...)
}

func (r *EdgeKeyRotation) Status() EdgeRotationStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.statusLocked()
}

func (r *EdgeKeyRotation) statusLocked() EdgeRotationStatus {
	status := EdgeRotationStatus{RotationStatus: RotationStatus{RootKeyID: r.state.RootID,
		CurrentKeyID: KeyIDForPublicKey(r.current.Public().(ed25519.PublicKey))}, ActivationPending: r.state.Trying}
	if r.pending != nil {
		pub := r.pending.Public().(ed25519.PublicKey)
		status.PendingKeyID = KeyIDForPublicKey(pub)
		status.PendingPublicKey = base64.StdEncoding.EncodeToString(pub)
	}
	return status
}

func (r *EdgeKeyRotation) Prepare() (EdgeRotationStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending != nil {
		return r.statusLocked(), nil
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return EdgeRotationStatus{}, fmt.Errorf("generating edge replacement: %w", err)
	}
	pem, err := MarshalPrivateKeyPEM(private)
	if err != nil {
		return EdgeRotationStatus{}, err
	}
	updated := r.state
	updated.Pending = pem
	if err := r.persist(updated); err != nil {
		return EdgeRotationStatus{}, err
	}
	r.pending = private
	return r.statusLocked(), nil
}

func (r *EdgeKeyRotation) Activate(keyID string) (EdgeRotationStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending == nil || KeyIDForPublicKey(r.pending.Public().(ed25519.PublicKey)) != keyID {
		return EdgeRotationStatus{}, ErrRotationInvalid
	}
	updated := r.state
	updated.Trying = true
	if err := r.persist(updated); err != nil {
		return EdgeRotationStatus{}, err
	}
	return r.statusLocked(), nil
}

func (r *EdgeKeyRotation) Confirm() (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.state.Trying {
		return false, nil
	}
	updated := r.state
	updated.Current, updated.Pending, updated.Trying = updated.Pending, nil, false
	if err := r.persist(updated); err != nil {
		return false, err
	}
	r.current, r.pending = r.pending, nil
	return true, nil
}

func (r *EdgeKeyRotation) RejectTrial() (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.state.Trying {
		return false, nil
	}
	updated := r.state
	updated.Trying = false
	if err := r.persist(updated); err != nil {
		return false, err
	}
	return true, nil
}

func (r *EdgeKeyRotation) Abort() (EdgeRotationStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	updated := r.state
	updated.Pending, updated.Trying = nil, false
	if err := r.persist(updated); err != nil {
		return EdgeRotationStatus{}, err
	}
	r.pending = nil
	return r.statusLocked(), nil
}

func (r *EdgeKeyRotation) persist(state edgeCredentialState) error {
	data, _ := json.Marshal(state)
	if err := WriteRotationState(r.path, data); err != nil {
		return err
	}
	r.state = state
	return nil
}
