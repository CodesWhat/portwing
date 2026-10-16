// SPDX-License-Identifier: MIT

package auth

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func edgeRotationFixture(t *testing.T) (string, string, []byte) {
	t.Helper()
	pem, _, err := GenerateKeyPair("bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "bootstrap.pem")
	if err := os.WriteFile(file, pem, 0o400); err != nil {
		t.Fatal(err)
	}
	return file, filepath.Join(t.TempDir(), "managed"), pem
}

func TestEdgeRotationRequiresSuccessfulTrialBeforeRetirement(t *testing.T) {
	t.Parallel()
	file, dir, original := edgeRotationFixture(t)
	r, err := NewEdgeKeyRotation(file, dir)
	if err != nil {
		t.Fatal(err)
	}
	old := KeyIDForPublicKey(r.SigningKey().Public().(ed25519.PublicKey))
	status, err := r.Prepare()
	if err != nil || status.CurrentKeyID != old || status.PendingKeyID == "" {
		t.Fatalf("prepare: %+v %v", status, err)
	}
	if KeyIDForPublicKey(r.SigningKey().Public().(ed25519.PublicKey)) != old {
		t.Fatal("preparation changed the active signer")
	}
	if changed, err := r.Confirm(); err != nil || changed {
		t.Fatalf("untried key cannot be confirmed: %v %v", changed, err)
	}
	if _, err := r.Activate("wrong-key"); err == nil {
		t.Fatal("activation must bind to the prepared key")
	}
	if _, err := r.Activate(status.PendingKeyID); err != nil {
		t.Fatal(err)
	}
	if KeyIDForPublicKey(r.SigningKey().Public().(ed25519.PublicKey)) != status.PendingKeyID {
		t.Fatal("trial did not select the replacement")
	}
	restarted, err := NewEdgeKeyRotation(file, dir)
	if err != nil {
		t.Fatal(err)
	}
	if KeyIDForPublicKey(restarted.SigningKey().Public().(ed25519.PublicKey)) != status.PendingKeyID {
		t.Fatal("restart lost the staged signer")
	}
	if changed, err := restarted.Confirm(); err != nil || !changed {
		t.Fatalf("confirm: %v %v", changed, err)
	}
	final, err := NewEdgeKeyRotation(file, dir)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status().CurrentKeyID != status.PendingKeyID || final.Status().PendingKeyID != "" {
		t.Fatalf("commit lost across restart: %+v", final.Status())
	}
	after, err := os.ReadFile(file)
	if err != nil || string(after) != string(original) {
		t.Fatal("bootstrap private key changed")
	}
	encoded, err := json.Marshal(final.Status())
	if err != nil || strings.Contains(string(encoded), "PRIVATE KEY") {
		t.Fatal("status exposed private material")
	}
}

func TestEdgeRotationRejectedTrialAndAbortPreserveOriginal(t *testing.T) {
	t.Parallel()
	file, dir, _ := edgeRotationFixture(t)
	r, err := NewEdgeKeyRotation(file, dir)
	if err != nil {
		t.Fatal(err)
	}
	old := r.Status().CurrentKeyID
	status, err := r.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	again, err := r.Prepare()
	if err != nil || again.PendingKeyID != status.PendingKeyID {
		t.Fatal("prepare retry generated another key")
	}
	if _, err := r.Activate(status.PendingKeyID); err != nil {
		t.Fatal(err)
	}
	if changed, err := r.RejectTrial(); err != nil || !changed {
		t.Fatalf("reject trial: %v %v", changed, err)
	}
	if KeyIDForPublicKey(r.SigningKey().Public().(ed25519.PublicKey)) != old {
		t.Fatal("failed trial discarded the old signer")
	}
	if r.Status().PendingKeyID != status.PendingKeyID {
		t.Fatal("failed trial discarded the recoverable replacement")
	}
	if _, err := r.Abort(); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewEdgeKeyRotation(file, dir)
	if err != nil || restarted.Status().CurrentKeyID != old || restarted.Status().PendingKeyID != "" {
		t.Fatalf("abort did not survive restart: %v", err)
	}
}

func TestEdgeRotationPersistenceFailureDoesNotLoseTrialKey(t *testing.T) {
	t.Parallel()
	file, dir, _ := edgeRotationFixture(t)
	r, err := NewEdgeKeyRotation(file, dir)
	if err != nil {
		t.Fatal(err)
	}
	status, err := r.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Activate(status.PendingKeyID); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dir, dir+"-offline"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Confirm(); err == nil {
		t.Fatal("expected durable commit to fail")
	}
	if KeyIDForPublicKey(r.SigningKey().Public().(ed25519.PublicKey)) != status.PendingKeyID {
		t.Fatal("failed local confirmation lost the key that the controller accepted")
	}
	if err := os.Rename(dir+"-offline", dir); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewEdgeKeyRotation(file, dir)
	if err != nil {
		t.Fatal(err)
	}
	if KeyIDForPublicKey(restarted.SigningKey().Public().(ed25519.PublicKey)) != status.PendingKeyID {
		t.Fatal("pending credential was not recoverable")
	}
	if _, err := restarted.Confirm(); err != nil {
		t.Fatal(err)
	}
}

func TestEdgeRotationRejectsIncompletePersistedState(t *testing.T) {
	t.Parallel()
	file, dir, pem := edgeRotationFixture(t)
	private, err := ParsePrivateKeyPEM(pem)
	if err != nil {
		t.Fatal(err)
	}
	root := KeyIDForPublicKey(private.Public().(ed25519.PublicKey))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"version": 1, "root_key_id": root})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "edge-credentials.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEdgeKeyRotation(file, dir); err == nil {
		t.Fatal("missing current key must not silently reactivate the bootstrap credential")
	}
}

func TestEdgeRotationRejectsCorruptOrWrongRootState(t *testing.T) {
	for _, name := range []string{"json", "version", "root", "current", "pending", "duplicate", "trial", "oversize", "public-file"} {
		t.Run(name, func(t *testing.T) {
			file, dir, pem := edgeRotationFixture(t)
			r, err := NewEdgeKeyRotation(file, dir)
			if err != nil {
				t.Fatal(err)
			}
			state := r.state
			mode := os.FileMode(0o600)
			switch name {
			case "version":
				state.Version = 2
			case "root":
				state.RootID = "other"
			case "current":
				state.Current = []byte("invalid")
			case "pending":
				state.Pending = []byte("invalid")
			case "duplicate":
				state.Pending = pem
			case "trial":
				state.Trying = true
			case "public-file":
				mode = 0o644
			}
			data, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if name == "json" {
				data = []byte("{")
			}
			if name == "oversize" {
				data = make([]byte, 65537)
			}
			if err := os.WriteFile(r.path, data, mode); err != nil {
				t.Fatal(err)
			}
			if _, err := NewEdgeKeyRotation(file, dir); err == nil {
				t.Fatal("accepted corrupt or unsafe managed credentials")
			}
		})
	}
	file, dir, _ := edgeRotationFixture(t)
	if _, err := NewEdgeKeyRotation(file+"-missing", dir); err == nil {
		t.Fatal("accepted missing bootstrap")
	}
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEdgeKeyRotation(file, dir); err == nil {
		t.Fatal("accepted file as managed directory")
	}
}

func TestEdgeRotationFailedWritesPreserveMemoryAndDisk(t *testing.T) {
	for _, operation := range []string{"prepare", "activate", "abort", "reject"} {
		t.Run(operation, func(t *testing.T) {
			file, dir, _ := edgeRotationFixture(t)
			r, err := NewEdgeKeyRotation(file, dir)
			if err != nil {
				t.Fatal(err)
			}
			if operation != "prepare" {
				if _, err := r.Prepare(); err != nil {
					t.Fatal(err)
				}
			}
			if operation == "reject" {
				if _, err := r.Activate(r.Status().PendingKeyID); err != nil {
					t.Fatal(err)
				}
			}
			before := r.Status()
			if err := os.Rename(dir, dir+"-offline"); err != nil {
				t.Fatal(err)
			}
			switch operation {
			case "prepare":
				_, err = r.Prepare()
			case "activate":
				_, err = r.Activate(before.PendingKeyID)
			case "abort":
				_, err = r.Abort()
			case "reject":
				_, err = r.RejectTrial()
			}
			if err == nil {
				t.Fatal("write failure was hidden")
			}
			if r.Status() != before {
				t.Fatal("write failure changed live credentials")
			}
			if err := os.Rename(dir+"-offline", dir); err != nil {
				t.Fatal(err)
			}
			restored, err := NewEdgeKeyRotation(file, dir)
			if err != nil || restored.Status() != before {
				t.Fatalf("write failure lost durable state: %v", err)
			}
		})
	}
	file, dir, _ := edgeRotationFixture(t)
	r, err := NewEdgeKeyRotation(file, dir)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := r.RejectTrial(); err != nil || changed {
		t.Fatal("non-trial rejection must be a no-op")
	}
}

func TestEdgeDirectorySyncFailureCannotActivateUndurableCredentials(t *testing.T) {
	file, dir, _ := edgeRotationFixture(t)
	r, err := NewEdgeKeyRotation(file, dir)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := r.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	realSync := syncRotationDirectory
	syncRotationDirectory = func(*os.File) error { return errors.New("fixture directory sync failed") }
	t.Cleanup(func() { syncRotationDirectory = realSync })
	if _, err := r.Activate(prepared.PendingKeyID); err == nil {
		t.Fatal("unsynced activation reported success")
	}
	if r.Status().ActivationPending {
		t.Fatal("unsynced activation changed live signer")
	}
	if _, err := NewEdgeKeyRotation(file, dir); err == nil {
		t.Fatal("restart accepted a signer before directory durability was confirmed")
	}
	syncRotationDirectory = realSync
	restarted, err := NewEdgeKeyRotation(file, dir)
	if err != nil {
		t.Fatal(err)
	}
	if KeyIDForPublicKey(restarted.SigningKey().Public().(ed25519.PublicKey)) != prepared.PendingKeyID {
		t.Fatal("restart did not reconcile the renamed trial")
	}
	syncRotationDirectory = func(*os.File) error { return errors.New("fixture directory sync failed") }
	if _, err := restarted.Confirm(); err == nil {
		t.Fatal("unsynced confirmation reported success")
	}
	if KeyIDForPublicKey(restarted.SigningKey().Public().(ed25519.PublicKey)) != prepared.PendingKeyID {
		t.Fatal("unresolved confirmation lost the accepted private key")
	}
	syncRotationDirectory = realSync
	if _, err := restarted.Confirm(); err != nil {
		t.Fatal(err)
	}
}
