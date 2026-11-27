// SPDX-License-Identifier: MIT

package auth

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestLoadOrCreateEnrolledKeyKeepsMarkerWhenFinalDirSyncFails(t *testing.T) {
	dir := t.TempDir()
	prev := syncRotationDirectory
	t.Cleanup(func() { syncRotationDirectory = prev })
	// The first sync follows the marker, the second follows the key link.
	calls := 0
	syncErr := errors.New("final directory sync failed")
	syncRotationDirectory = func(d *os.File) error {
		calls++
		if calls == 2 {
			return syncErr
		}
		return prev(d)
	}
	key, err := LoadOrCreateEnrolledKey(dir, true)
	if !errors.Is(err, syncErr) || !strings.Contains(err.Error(), "writing enrolled key") {
		t.Fatalf("want the sync error, got %v", err)
	}
	if !key.Pending {
		t.Fatalf("a linked key must report pending: %+v", key)
	}
	if _, statErr := os.Lstat(key.Path); statErr != nil {
		t.Fatalf("the key was linked and must stay: %v", statErr)
	}
	if _, statErr := os.Lstat(key.MarkerPath); statErr != nil {
		t.Fatalf("the marker must outlive a published key: %v", statErr)
	}
	syncRotationDirectory = prev
	again, err := LoadOrCreateEnrolledKey(dir, false)
	if err != nil || !again.Pending {
		t.Fatalf("the next start must still see a pending key: %+v %v", again, err)
	}
}

func TestWasPublishedOnlyForPostPublicationErrors(t *testing.T) {
	if wasPublished(errors.New("plain")) {
		t.Fatal("a plain error is not a publication")
	}
	pe := &publishedError{err: errors.New("sync")}
	if !wasPublished(fmt.Errorf("wrapped: %w", pe)) || pe.Error() != "sync" {
		t.Fatal("a publishedError must be found through wrapping and keep its message")
	}
}
