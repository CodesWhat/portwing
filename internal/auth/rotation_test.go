// SPDX-License-Identifier: MIT

package auth

import (
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type rotationFixture struct {
	registry *KeyRegistry
	path     string
	dir      string
	old      ed25519.PublicKey
	other    ed25519.PublicKey
	next     ed25519.PublicKey
}

func newRotationFixture(t *testing.T) rotationFixture {
	t.Helper()
	old, _ := genPubKeyB64(t)
	other, _ := genPubKeyB64(t)
	next, _ := genPubKeyB64(t)
	path := writeKeyFile(t, AuthorizedKeyLine(old, "controller")+"\n"+AuthorizedKeyLine(other, "independent")+"\n", 0o600)
	dir := filepath.Join(t.TempDir(), "managed")
	r := NewKeyRegistry(path)
	if err := r.EnableRotation(dir); err != nil {
		t.Fatal(err)
	}
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	return rotationFixture{r, path, dir, old, other, next}
}

func requireRotationKey(t *testing.T, r *KeyRegistry, pub ed25519.PublicKey, want bool) {
	t.Helper()
	_, got := r.LookupByID(KeyIDForPublicKey(pub))
	if got != want {
		t.Fatalf("key %s accepted=%v, want %v", KeyIDForPublicKey(pub), got, want)
	}
}

func TestRotationOverlapsThenRequiresReplacementProof(t *testing.T) {
	t.Parallel()
	f := newRotationFixture(t)
	before, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	status, err := f.registry.PrepareRotation(KeyIDForPublicKey(f.old), f.next)
	if err != nil || status.CurrentKeyID != KeyIDForPublicKey(f.old) || status.PendingKeyID != KeyIDForPublicKey(f.next) {
		t.Fatalf("prepare: %+v, %v", status, err)
	}
	requireRotationKey(t, f.registry, f.old, true)
	requireRotationKey(t, f.registry, f.next, true)
	if _, err := f.registry.CommitRotation(KeyIDForPublicKey(f.old)); !errors.Is(err, ErrRotationForbidden) {
		t.Fatalf("old key must not prove its replacement: %v", err)
	}
	status, err = f.registry.CommitRotation(KeyIDForPublicKey(f.next))
	if err != nil || status.CurrentKeyID != KeyIDForPublicKey(f.next) || status.PendingKeyID != "" {
		t.Fatalf("commit: %+v, %v", status, err)
	}
	requireRotationKey(t, f.registry, f.old, false)
	requireRotationKey(t, f.registry, f.next, true)
	requireRotationKey(t, f.registry, f.other, true)
	after, err := os.ReadFile(f.path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("operator key file changed: %v", err)
	}
	if err := f.registry.Load(); err != nil {
		t.Fatal(err)
	}
	requireRotationKey(t, f.registry, f.old, false)
	if _, err := f.registry.CommitRotation(KeyIDForPublicKey(f.next)); err != nil {
		t.Fatalf("commit retry must be idempotent: %v", err)
	}
}

func TestRotationSurvivesRestartAndOperatorRevocation(t *testing.T) {
	t.Parallel()
	f := newRotationFixture(t)
	if _, err := f.registry.PrepareRotation(KeyIDForPublicKey(f.old), f.next); err != nil {
		t.Fatal(err)
	}
	restart := func() *KeyRegistry {
		t.Helper()
		r := NewKeyRegistry(f.path)
		if err := r.EnableRotation(f.dir); err != nil {
			t.Fatal(err)
		}
		if err := r.Load(); err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := restart()
	requireRotationKey(t, r, f.old, true)
	requireRotationKey(t, r, f.next, true)
	if _, err := r.CommitRotation(KeyIDForPublicKey(f.next)); err != nil {
		t.Fatal(err)
	}
	r = restart()
	requireRotationKey(t, r, f.old, false)
	requireRotationKey(t, r, f.next, true)
	if err := os.WriteFile(f.path, []byte(AuthorizedKeyLine(f.other, "independent")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	requireRotationKey(t, r, f.next, false)
	requireRotationKey(t, r, f.other, true)
}

func TestRotationAbortAndSecondRotation(t *testing.T) {
	t.Parallel()
	f := newRotationFixture(t)
	oldID := KeyIDForPublicKey(f.old)
	if _, err := f.registry.PrepareRotation(oldID, f.next); err != nil {
		t.Fatal(err)
	}
	if _, err := f.registry.AbortRotation(KeyIDForPublicKey(f.next)); !errors.Is(err, ErrRotationForbidden) {
		t.Fatalf("pending key may not cancel the current key: %v", err)
	}
	if _, err := f.registry.AbortRotation(oldID); err != nil {
		t.Fatal(err)
	}
	requireRotationKey(t, f.registry, f.old, true)
	requireRotationKey(t, f.registry, f.next, false)
	if _, err := f.registry.PrepareRotation(oldID, f.next); err != nil {
		t.Fatal(err)
	}
	if _, err := f.registry.CommitRotation(KeyIDForPublicKey(f.next)); err != nil {
		t.Fatal(err)
	}
	third, _ := genPubKeyB64(t)
	if _, err := f.registry.PrepareRotation(KeyIDForPublicKey(f.next), third); err != nil {
		t.Fatal(err)
	}
	if _, err := f.registry.CommitRotation(KeyIDForPublicKey(third)); err != nil {
		t.Fatal(err)
	}
	requireRotationKey(t, f.registry, f.old, false)
	requireRotationKey(t, f.registry, f.next, false)
	requireRotationKey(t, f.registry, third, true)
}

func TestRotationRejectsConflictsAndUnknownCallers(t *testing.T) {
	t.Parallel()
	f := newRotationFixture(t)
	oldID := KeyIDForPublicKey(f.old)
	for _, pub := range []ed25519.PublicKey{f.old, f.other, {1, 2, 3}} {
		if _, err := f.registry.PrepareRotation(oldID, pub); !errors.Is(err, ErrRotationInvalid) {
			t.Fatalf("invalid successor must fail: %v", err)
		}
	}
	if _, err := f.registry.PrepareRotation("unknown", f.next); !errors.Is(err, ErrRotationForbidden) {
		t.Fatalf("unknown signer: %v", err)
	}
	if _, err := f.registry.PrepareRotation(oldID, f.next); err != nil {
		t.Fatal(err)
	}
	if _, err := f.registry.PrepareRotation(oldID, f.next); err != nil {
		t.Fatalf("prepare retry must be idempotent: %v", err)
	}
	third, _ := genPubKeyB64(t)
	if _, err := f.registry.PrepareRotation(oldID, third); !errors.Is(err, ErrRotationConflict) {
		t.Fatalf("different pending successor: %v", err)
	}
	if _, err := f.registry.PrepareRotation(KeyIDForPublicKey(f.other), f.next); !errors.Is(err, ErrRotationInvalid) {
		t.Fatalf("successor already belongs to another root: %v", err)
	}
	if _, err := f.registry.PrepareRotation(KeyIDForPublicKey(f.next), third); !errors.Is(err, ErrRotationForbidden) {
		t.Fatalf("unproven successor cannot delegate: %v", err)
	}
}

func TestRotationPersistenceFailurePreservesWorkingKeys(t *testing.T) {
	t.Parallel()
	f := newRotationFixture(t)
	if _, err := f.registry.PrepareRotation(KeyIDForPublicKey(f.old), f.next); err != nil {
		t.Fatal(err)
	}
	// An unavailable destination deterministically fails on root and Unix too.
	if err := os.Rename(f.dir, f.dir+"-offline"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.registry.CommitRotation(KeyIDForPublicKey(f.next)); err == nil {
		t.Fatal("commit must fail when state cannot be persisted")
	}
	requireRotationKey(t, f.registry, f.old, true)
	requireRotationKey(t, f.registry, f.next, true)
}

func TestRotationSerializesReloadAndCompetingPreparation(t *testing.T) {
	t.Parallel()
	f := newRotationFixture(t)
	third, _ := genPubKeyB64(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, pub := range []ed25519.PublicKey{f.next, third} {
		wg.Go(func() { _, err := f.registry.PrepareRotation(KeyIDForPublicKey(f.old), pub); errs <- err })
	}
	wg.Go(func() {
		for range 10 {
			if err := f.registry.Load(); err != nil {
				t.Error(err)
			}
		}
	})
	wg.Wait()
	close(errs)
	var successes, conflicts int
	for err := range errs {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrRotationConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("prepare results: successes=%d conflicts=%d", successes, conflicts)
	}
	requireRotationKey(t, f.registry, f.old, true)
	if f.registry.Len() != 3 {
		t.Fatalf("reload lost the pending key: %d keys", f.registry.Len())
	}
}
