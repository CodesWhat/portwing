// SPDX-License-Identifier: MIT

package auth

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

var (
	ErrRotationDisabled  = errors.New("managed key rotation is disabled")
	ErrRotationForbidden = errors.New("key cannot perform this rotation operation")
	ErrRotationConflict  = errors.New("a different replacement key is already pending")
	ErrRotationInvalid   = errors.New("invalid replacement key")
)

const maxRotationStateBytes = 1024 * 1024

type RotationStatus struct {
	RootKeyID    string `json:"root_key_id"`
	CurrentKeyID string `json:"current_key_id"`
	PendingKeyID string `json:"pending_key_id,omitempty"`
}

type rotationKeys struct {
	Current ed25519.PublicKey `json:"current"`
	Pending ed25519.PublicKey `json:"pending,omitempty"`
}

type rotationState struct {
	Version int                     `json:"version"`
	Keys    map[string]rotationKeys `json:"keys"`
}

type rotationStore struct {
	path string
	keys map[string]rotationKeys
}

func (r *KeyRegistry) EnableRotation(dir string) error {
	if r.filePath == "" {
		return fmt.Errorf("managed rotation requires an authorized_keys file")
	}
	if err := PrepareRotationDirectory(dir); err != nil {
		return err
	}
	store := &rotationStore{path: filepath.Join(dir, "authorized-rotation.json"), keys: make(map[string]rotationKeys)}
	f, err := openCredentialFile(store.path, "rotation state")
	if err == nil {
		defer f.Close()
		data, readErr := io.ReadAll(io.LimitReader(f, maxRotationStateBytes+1))
		if readErr != nil {
			return fmt.Errorf("reading rotation state: %w", readErr)
		}
		if len(data) > maxRotationStateBytes {
			return fmt.Errorf("rotation state exceeds %d bytes", maxRotationStateBytes)
		}
		var state rotationState
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("decoding rotation state: %w", err)
		}
		if state.Version != 1 || state.Keys == nil {
			return fmt.Errorf("unsupported rotation state")
		}
		store.keys = state.Keys
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rotation != nil {
		return fmt.Errorf("managed rotation is already configured")
	}
	r.rotation = store
	return nil
}

func PrepareRotationDirectory(dir string) error {
	if dir == "" {
		return fmt.Errorf("rotation directory must not be empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating rotation directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("checking rotation directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("rotation directory must be a directory with mode 0700")
	}
	return nil
}

func (r *KeyRegistry) RotationStatus(caller string) (RotationStatus, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	root, keys, err := r.rotationForCaller(caller)
	return rotationStatus(root, keys), err
}

func (r *KeyRegistry) rotationForCaller(caller string) (string, rotationKeys, error) {
	if r.rotation == nil {
		return "", rotationKeys{}, ErrRotationDisabled
	}
	if _, ok := r.keys[caller]; !ok {
		return "", rotationKeys{}, ErrRotationForbidden
	}
	for root, bootstrap := range r.roots {
		keys, ok := r.rotation.keys[root]
		if !ok {
			keys.Current = ed25519.PublicKey(bootstrap.PubKey)
		}
		if KeyIDForPublicKey(keys.Current) == caller || (len(keys.Pending) != 0 && KeyIDForPublicKey(keys.Pending) == caller) {
			return root, keys, nil
		}
	}
	return "", rotationKeys{}, ErrRotationForbidden
}

func rotationStatus(root string, keys rotationKeys) RotationStatus {
	status := RotationStatus{RootKeyID: root}
	if len(keys.Current) != 0 {
		status.CurrentKeyID = KeyIDForPublicKey(keys.Current)
	}
	if len(keys.Pending) != 0 {
		status.PendingKeyID = KeyIDForPublicKey(keys.Pending)
	}
	return status
}

func (r *KeyRegistry) PrepareRotation(caller string, replacement ed25519.PublicKey) (RotationStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	root, keys, err := r.rotationForCaller(caller)
	if err != nil {
		return RotationStatus{}, err
	}
	if caller != KeyIDForPublicKey(keys.Current) {
		return RotationStatus{}, ErrRotationForbidden
	}
	if len(replacement) != ed25519.PublicKeySize {
		return RotationStatus{}, ErrRotationInvalid
	}
	nextID := KeyIDForPublicKey(replacement)
	if len(keys.Pending) != 0 {
		if KeyIDForPublicKey(keys.Pending) == nextID {
			return rotationStatus(root, keys), nil
		}
		return RotationStatus{}, ErrRotationConflict
	}
	if _, ok := r.keys[nextID]; ok {
		return RotationStatus{}, ErrRotationInvalid
	}
	if _, ok := r.roots[nextID]; ok {
		return RotationStatus{}, ErrRotationInvalid
	}
	keys.Pending = append(ed25519.PublicKey(nil), replacement...)
	return r.persistRotation(root, keys)
}

func (r *KeyRegistry) CommitRotation(caller string) (RotationStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	root, keys, err := r.rotationForCaller(caller)
	if err != nil {
		return RotationStatus{}, err
	}
	if len(keys.Pending) == 0 {
		if caller == KeyIDForPublicKey(keys.Current) {
			return rotationStatus(root, keys), nil
		}
		return RotationStatus{}, ErrRotationForbidden
	}
	if caller != KeyIDForPublicKey(keys.Pending) {
		return RotationStatus{}, ErrRotationForbidden
	}
	keys.Current, keys.Pending = keys.Pending, nil
	return r.persistRotation(root, keys)
}

func (r *KeyRegistry) AbortRotation(caller string) (RotationStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	root, keys, err := r.rotationForCaller(caller)
	if err != nil {
		return RotationStatus{}, err
	}
	if caller != KeyIDForPublicKey(keys.Current) {
		return RotationStatus{}, ErrRotationForbidden
	}
	if len(keys.Pending) == 0 {
		return rotationStatus(root, keys), nil
	}
	keys.Pending = nil
	return r.persistRotation(root, keys)
}

func (r *KeyRegistry) persistRotation(root string, keys rotationKeys) (RotationStatus, error) {
	updated := make(map[string]rotationKeys, len(r.rotation.keys)+1)
	for id, value := range r.rotation.keys {
		updated[id] = value
	}
	updated[root] = keys
	merged, err := overlayRotationKeys(r.roots, updated)
	if err != nil {
		return RotationStatus{}, err
	}
	data, err := json.Marshal(rotationState{Version: 1, Keys: updated})
	if err != nil {
		return RotationStatus{}, fmt.Errorf("encoding rotation state: %w", err)
	}
	if len(data) > maxRotationStateBytes {
		return RotationStatus{}, fmt.Errorf("rotation state exceeds %d bytes", maxRotationStateBytes)
	}
	if err := WriteRotationState(r.rotation.path, data); err != nil {
		return RotationStatus{}, err
	}
	r.rotation.keys = updated
	r.keys = merged
	return rotationStatus(root, keys), nil
}

func overlayRotationKeys(roots map[string]*AuthorizedKey, rotations map[string]rotationKeys) (map[string]*AuthorizedKey, error) {
	merged := make(map[string]*AuthorizedKey, len(roots)+len(rotations))
	for id, key := range roots {
		merged[id] = key
	}
	for root, keys := range rotations {
		id, err := hex.DecodeString(root)
		if err != nil || len(id) != 8 || len(keys.Current) != ed25519.PublicKeySize || (len(keys.Pending) != 0 && len(keys.Pending) != ed25519.PublicKeySize) {
			return nil, fmt.Errorf("invalid persisted rotation for root %q", root)
		}
		if roots[root] == nil {
			continue
		}
		delete(merged, root)
	}
	for root, keys := range rotations {
		if roots[root] == nil {
			continue
		}
		for _, pub := range []ed25519.PublicKey{keys.Current, keys.Pending} {
			if len(pub) == 0 {
				continue
			}
			id := KeyIDForPublicKey(pub)
			if (roots[id] != nil && id != root) || merged[id] != nil {
				return nil, fmt.Errorf("rotation key %s conflicts with another authorized key", id)
			}
			merged[id] = &AuthorizedKey{KeyID: id, PubKey: pub, Comment: "managed:" + root}
		}
	}
	return merged, nil
}

func WriteRotationState(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".rotation-")
	if err != nil {
		return fmt.Errorf("creating rotation state: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing rotation state: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("syncing rotation state: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing rotation state: %w", err)
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("replacing rotation state: %w", err)
	}
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		if err := dir.Sync(); err != nil {
			slog.Warn("rotation state replaced but directory sync failed", "error", err)
		}
		_ = dir.Close()
	}
	return nil
}
