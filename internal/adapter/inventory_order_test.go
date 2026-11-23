// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestInventoryOperationsDoNotInterleave pins the ordering between the initial
// inventory build and the periodic refresh: a build that is still inspecting
// when a refresh fires must not commit older state over the refresh's, and the
// sync callback must go out before the refresh's diff.
func TestInventoryOperationsDoNotInterleave(t *testing.T) {
	t.Parallel()

	client, fixture, shutdown := newDynamicDockerClient(t)
	defer shutdown()
	fixture.Set(buildInventorySnapshot([]fixtureContainer{{id: "c1", image: "nginx:1.0", state: "running"}}))
	manager := NewContainerManager(client, "test-agent", nil)

	inspecting := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	fixture.SetOnInspect(func() {
		once.Do(func() {
			close(inspecting)
			<-release
		})
	})

	var (
		orderMu sync.Mutex
		order   []string
	)
	record := func(ev string) {
		orderMu.Lock()
		defer orderMu.Unlock()
		order = append(order, ev)
	}

	var syncedStatus string
	buildDone := make(chan error, 1)
	go func() {
		_, err := manager.BuildInventoryThen(context.Background(), func(cs []Container) {
			syncedStatus = cs[0].Status
			record("sync")
		})
		buildDone <- err
	}()
	<-inspecting

	// Docker moves on while the build is blocked mid-inspect.
	fixture.Set(buildInventorySnapshot([]fixtureContainer{{id: "c1", image: "nginx:1.0", state: "exited"}}))

	refreshDone := make(chan []Container, 1)
	go func() {
		_, updated, _, err := manager.Refresh(context.Background())
		if err != nil {
			t.Errorf("Refresh: %v", err)
		}
		record("refresh")
		refreshDone <- updated
	}()

	select {
	case <-refreshDone:
		t.Fatal("refresh committed while the initial build was still in flight")
	case <-time.After(150 * time.Millisecond):
	}

	// Requests are still served from the last committed inventory meanwhile.
	done := make(chan struct{})
	go func() { manager.GetContainers(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reads blocked behind the in-flight inventory operation")
	}

	close(release)
	if err := <-buildDone; err != nil {
		t.Fatalf("BuildInventoryThen: %v", err)
	}
	var updated []Container
	select {
	case updated = <-refreshDone:
	case <-time.After(10 * time.Second):
		t.Fatal("refresh never ran after the build finished")
	}

	if got := manager.GetContainers(); len(got) != 1 || got[0].Status != "stopped" {
		t.Fatalf("final inventory = %+v, want the refresh's newer stopped state", got)
	}
	if len(updated) != 1 {
		t.Fatalf("refresh diff = %v, want one update against the build's snapshot", updated)
	}
	orderMu.Lock()
	defer orderMu.Unlock()
	if len(order) != 2 || order[0] != "sync" || order[1] != "refresh" {
		t.Fatalf("order = %v, want the sync callback before the refresh commit", order)
	}
	if syncedStatus == "" {
		t.Fatal("sync callback saw no container")
	}
}

func TestInventoryOperationHonoursContextWhileWaiting(t *testing.T) {
	t.Parallel()

	client, fixture, shutdown := newDynamicDockerClient(t)
	defer shutdown()
	fixture.Set(buildInventorySnapshot([]fixtureContainer{{id: "c1", image: "nginx:1.0", state: "running"}}))
	manager := NewContainerManager(client, "test-agent", nil)

	if err := manager.acquireOp(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := manager.BuildInventoryThen(ctx, nil); err == nil {
		t.Fatal("BuildInventory should fail when its context ends while waiting")
	}
	if _, _, _, err := manager.Refresh(ctx); err == nil {
		t.Fatal("Refresh should fail when its context ends while waiting")
	}
	manager.releaseOp()
	if _, err := manager.BuildInventoryThen(context.Background(), nil); err != nil {
		t.Fatalf("BuildInventory after release: %v", err)
	}
}
