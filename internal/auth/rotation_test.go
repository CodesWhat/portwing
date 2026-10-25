// SPDX-License-Identifier: MIT

package auth

import (
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestRotationDirectoryRequiresOwnerAccess(t *testing.T) {
	for _, mode := range []os.FileMode{0o500, 0o600, 0o750} {
		t.Run(mode.String(), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "private")
			if err := os.Mkdir(dir, mode); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				// #nosec G302 -- restore owner search permission on a private test directory for cleanup.
				if err := os.Chmod(dir, 0o700); err != nil {
					t.Error(err)
				}
			})
			if err := PrepareRotationDirectory(dir); err == nil {
				t.Fatalf("accepted unusable or shared directory %o", mode)
			}
		})
	}
}

func TestRotationStatusAndDisabledOperations(t *testing.T) {
	r := NewKeyRegistry("")
	if err := r.EnableRotation(t.TempDir()); err == nil {
		t.Fatal("accepted missing operator authority")
	}
	if _, err := r.RotationStatus("unknown"); !errors.Is(err, ErrRotationDisabled) {
		t.Fatal(err)
	}
	if _, err := r.CommitRotation("unknown"); !errors.Is(err, ErrRotationDisabled) {
		t.Fatal(err)
	}
	if _, err := r.AbortRotation("unknown"); !errors.Is(err, ErrRotationDisabled) {
		t.Fatal(err)
	}
	f := newRotationFixture(t)
	old := KeyIDForPublicKey(f.old)
	if err := f.registry.EnableRotation(f.dir); err == nil {
		t.Fatal("enabled twice")
	}
	status, err := f.registry.RotationStatus(old)
	if err != nil || status.CurrentKeyID != old || status.PendingKeyID != "" {
		t.Fatalf("status: %+v %v", status, err)
	}
	if _, err := f.registry.AbortRotation(old); err != nil {
		t.Fatal(err)
	}
	if _, err := f.registry.RotationStatus("unknown"); !errors.Is(err, ErrRotationForbidden) {
		t.Fatal(err)
	}
}

func TestRotationRejectsInvalidPersistentState(t *testing.T) {
	for _, name := range []string{"json", "version", "missing", "oversize", "bad-root", "bad-current", "bad-pending", "collision"} {
		t.Run(name, func(t *testing.T) {
			f := newRotationFixture(t)
			root := KeyIDForPublicKey(f.old)
			state := rotationState{Version: 1, Keys: map[string]rotationKeys{root: {Current: f.old}}}
			switch name {
			case "version":
				state.Version = 2
			case "missing":
				state.Keys = nil
			case "bad-root":
				state.Keys = map[string]rotationKeys{"bad": {Current: f.old}}
			case "bad-current":
				state.Keys[root] = rotationKeys{Current: []byte("bad")}
			case "bad-pending":
				state.Keys[root] = rotationKeys{Current: f.old, Pending: []byte("bad")}
			case "collision":
				state.Keys[root] = rotationKeys{Current: f.other}
			}
			data, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if name == "json" {
				data = []byte("{")
			}
			if name == "oversize" {
				data = make([]byte, maxRotationStateBytes+1)
			}
			if err := os.WriteFile(filepath.Join(f.dir, "authorized-rotation.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			r := NewKeyRegistry(f.path)
			err = r.EnableRotation(f.dir)
			if err == nil {
				err = r.Load()
			}
			if err == nil {
				t.Fatal("accepted corrupt managed authorization")
			}
		})
	}
}

func TestRotationStateRenameFailureLeavesOriginal(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "occupied")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteRotationState(target, []byte("replacement")); err == nil {
		t.Fatal("rename must fail")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "occupied" {
		t.Fatalf("temporary credentials leaked: %v", err)
	}
}

func TestRemovedRotationRootCannotRestoreManagedDescendants(t *testing.T) {
	for _, committed := range []bool{false, true} {
		for _, restartOnRemoval := range []bool{false, true} {
			t.Run(fmt.Sprintf("committed=%v/restart=%v", committed, restartOnRemoval), func(t *testing.T) {
				f := newRotationFixture(t)
				old := KeyIDForPublicKey(f.old)
				if _, err := f.registry.PrepareRotation(old, f.next); err != nil {
					t.Fatal(err)
				}
				if committed {
					if _, err := f.registry.CommitRotation(KeyIDForPublicKey(f.next)); err != nil {
						t.Fatal(err)
					}
				}
				independent, _ := genPubKeyB64(t)
				if _, err := f.registry.PrepareRotation(KeyIDForPublicKey(f.other), independent); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f.path, []byte(AuthorizedKeyLine(f.other, "independent")+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				r := f.registry
				if restartOnRemoval {
					r = NewKeyRegistry(f.path)
					if err := r.EnableRotation(f.dir); err != nil {
						t.Fatal(err)
					}
				}
				if err := r.Load(); err != nil {
					t.Fatal(err)
				}
				requireRotationKey(t, r, f.next, false)
				if err := os.WriteFile(f.path, []byte(AuthorizedKeyLine(f.old, "fresh-bootstrap")+"\n"+AuthorizedKeyLine(f.other, "independent")+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				r = NewKeyRegistry(f.path)
				if err := r.EnableRotation(f.dir); err != nil {
					t.Fatal(err)
				}
				if err := r.Load(); err != nil {
					t.Fatal(err)
				}
				requireRotationKey(t, r, f.old, true)
				requireRotationKey(t, r, f.next, false)
				requireRotationKey(t, r, independent, true)
			})
		}
	}
}

func TestRootPruningFailureDoesNotPublishPartialRegistry(t *testing.T) {
	f := newRotationFixture(t)
	if _, err := f.registry.PrepareRotation(KeyIDForPublicKey(f.old), f.next); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path, []byte(AuthorizedKeyLine(f.other, "independent")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.dir, f.dir+"-offline"); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Load(); err == nil {
		t.Fatal("reload must report failed durable pruning")
	}
	requireRotationKey(t, f.registry, f.next, true)
	if len(f.registry.rotation.keys) != 1 {
		t.Fatal("failed reload changed durable state snapshot")
	}
	if err := os.Rename(f.dir+"-offline", f.dir); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.Load(); err != nil {
		t.Fatal(err)
	}
	requireRotationKey(t, f.registry, f.next, false)
}

func TestDirectorySyncFailureCannotReportRotationCompletion(t *testing.T) {
	f := newRotationFixture(t)
	old := KeyIDForPublicKey(f.old)
	next := KeyIDForPublicKey(f.next)
	if _, err := f.registry.PrepareRotation(old, f.next); err != nil {
		t.Fatal(err)
	}
	realSync := syncRotationDirectory
	syncRotationDirectory = func(*os.File) error { return errors.New("fixture directory sync failed") }
	t.Cleanup(func() { syncRotationDirectory = realSync })
	if _, err := f.registry.CommitRotation(next); err == nil {
		t.Fatal("unsynced commit reported successful completion")
	}
	status, err := f.registry.RotationStatus(old)
	if err != nil || status.CurrentKeyID != old || status.PendingKeyID != next {
		t.Fatalf("unresolved write reported committed: %+v %v", status, err)
	}
	restored := NewKeyRegistry(f.path)
	if err := restored.EnableRotation(f.dir); err == nil {
		t.Fatal("startup accepted state without confirming directory durability")
	}
	syncRotationDirectory = realSync
	if _, err := f.registry.CommitRotation(next); err != nil {
		t.Fatal(err)
	}
	requireRotationKey(t, f.registry, f.old, false)
}

type failingRotationFile struct {
	*os.File
	stage string
}

func (f failingRotationFile) Write(data []byte) (int, error) {
	if f.stage == "write" {
		return 0, errors.New("fixture write failure")
	}
	return f.File.Write(data)
}
func (f failingRotationFile) Sync() error {
	if f.stage == "sync" {
		return errors.New("fixture file sync failure")
	}
	return f.File.Sync()
}
func (f failingRotationFile) Close() error {
	err := f.File.Close()
	if f.stage == "close" {
		return errors.New("fixture close failure")
	}
	return err
}

func TestRotationWriteFailuresNeverReplaceDurableState(t *testing.T) {
	originalCreate := createRotationStateFile
	t.Cleanup(func() { createRotationStateFile = originalCreate })
	for _, stage := range []string{"write", "sync", "close"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "state.json")
			if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
				t.Fatal(err)
			}
			createRotationStateFile = func(dir, pattern string) (rotationStateFile, error) {
				f, err := os.CreateTemp(dir, pattern)
				return failingRotationFile{f, stage}, err
			}
			if err := WriteRotationState(target, []byte("replacement")); err == nil {
				t.Fatal("write failure reported success")
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "original" {
				t.Fatalf("old state lost: %v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("temporary credentials leaked: %v", err)
			}
		})
	}
	if err := syncRotationStateDirectory(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing directory sync reported success")
	}
}

func TestRotationRejectsUnsafeStorageAndAnotherRootsBootstrap(t *testing.T) {
	if err := PrepareRotationDirectory(""); err == nil {
		t.Fatal("accepted empty managed directory")
	}
	f := newRotationFixture(t)
	if err := NewKeyRegistry(f.path).EnableRotation(""); err == nil {
		t.Fatal("accepted empty registry directory")
	}
	path := filepath.Join(f.dir, "authorized-rotation.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"keys":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := NewKeyRegistry(f.path).EnableRotation(f.dir); err == nil {
		t.Fatal("accepted exposed registry state")
	}
	replacement, _ := genPubKeyB64(t)
	if _, err := f.registry.PrepareRotation(KeyIDForPublicKey(f.other), replacement); err != nil {
		t.Fatal(err)
	}
	if _, err := f.registry.CommitRotation(KeyIDForPublicKey(replacement)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.registry.PrepareRotation(KeyIDForPublicKey(f.old), f.other); !errors.Is(err, ErrRotationInvalid) {
		t.Fatalf("reused another root's bootstrap: %v", err)
	}
}

func TestRotationCannotPublishStateTooLargeToReload(t *testing.T) {
	f := newRotationFixture(t)
	state := rotationState{Version: 1, Keys: map[string]rotationKeys{}}
	empty, _ := json.Marshal(state)
	state.Keys[KeyIDForPublicKey(f.old)] = rotationKeys{Current: f.old}
	one, _ := json.Marshal(state)
	entrySize := len(one) - len(empty) + 1
	count := (maxRotationStateBytes - len(empty) + 1) / entrySize
	var roots strings.Builder
	roots.WriteString(AuthorizedKeyLine(f.old, "target") + "\n")
	state.Keys[KeyIDForPublicKey(f.other)] = rotationKeys{Current: f.other}
	roots.WriteString(AuthorizedKeyLine(f.other, "independent") + "\n")
	for i := 2; i < count; i++ {
		pub := make(ed25519.PublicKey, 32)
		binary.LittleEndian.PutUint64(pub, uint64(i+100))
		state.Keys[KeyIDForPublicKey(pub)] = rotationKeys{Current: pub}
		roots.WriteString(AuthorizedKeyLine(pub, "fleet") + "\n")
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	pendingFieldSize := len(`,"pending":"`) + 44 + 1
	if maxRotationStateBytes-len(data) >= pendingFieldSize {
		pending := make(ed25519.PublicKey, 32)
		binary.LittleEndian.PutUint64(pending, 1_000_000_000)
		state.Keys[KeyIDForPublicKey(f.other)] = rotationKeys{Current: f.other, Pending: pending}
		data, err = json.Marshal(state)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(data) > maxRotationStateBytes || maxRotationStateBytes-len(data) >= pendingFieldSize {
		t.Fatalf("bad near-cap fixture size %d", len(data))
	}
	if err := os.WriteFile(f.path, []byte(roots.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(f.dir, "authorized-rotation.json")
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewKeyRegistry(f.path)
	if err := r.EnableRotation(f.dir); err != nil {
		t.Fatal(err)
	}
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.PrepareRotation(KeyIDForPublicKey(f.old), f.next); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized mutation: %v", err)
	}
	after, err := os.ReadFile(statePath)
	if err != nil || string(after) != string(data) {
		t.Fatalf("failed mutation changed durable state: %v", err)
	}
	requireRotationKey(t, r, f.old, true)
	requireRotationKey(t, r, f.next, false)
}

func TestUnreadableManagedStateNeverBootstrapsEitherMode(t *testing.T) {
	f := newRotationFixture(t)
	if _, err := f.registry.PrepareRotation(KeyIDForPublicKey(f.old), f.next); err != nil {
		t.Fatal(err)
	}
	file, dir, _ := edgeRotationFixture(t)
	edge, err := NewEdgeKeyRotation(file, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := edge.Prepare(); err != nil {
		t.Fatal(err)
	}
	realRead := readRotationState
	readRotationState = func(io.Reader) ([]byte, error) { return nil, errors.New("fixture storage read failed") }
	t.Cleanup(func() { readRotationState = realRead })
	if err := NewKeyRegistry(f.path).EnableRotation(f.dir); err == nil {
		t.Fatal("ignored unreadable authorization state")
	}
	if _, err := NewEdgeKeyRotation(file, dir); err == nil {
		t.Fatal("ignored unreadable private state")
	}
}
