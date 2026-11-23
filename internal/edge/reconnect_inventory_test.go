// SPDX-License-Identifier: MIT

package edge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/codeswhat/portwing/internal/adapter"
	"github.com/codeswhat/portwing/internal/config"
	"github.com/codeswhat/portwing/internal/protocol"
)

// syncAdapter models an adapter whose OnConnect inventory sync blocks on a
// slow Docker inspect. The first call blocks until gate closes or its ctx ends;
// later calls return at once. Either way a blocked call tries to send one
// container-sync frame afterwards, the way a late Docker reply would.
type syncAdapter struct {
	*fakeAdapter
	entered  chan struct{}
	gate     chan struct{}
	calls    atomic.Int32
	finished atomic.Bool
	ctxErr   atomic.Value // error seen when the blocked call woke
}

func (a *syncAdapter) OnConnect(ctx context.Context, sender adapter.MessageSender) error {
	if a.calls.Add(1) > 1 {
		return nil
	}
	close(a.entered)
	select {
	case <-a.gate:
	case <-ctx.Done():
		a.ctxErr.Store(ctx.Err())
	}
	_ = sender.SendTypedMessage(protocol.TypeDDContainerSync, map[string]any{"containers": []any{}})
	a.finished.Store(true)
	return nil
}

func newSyncAdapter() *syncAdapter {
	return &syncAdapter{
		fakeAdapter: &fakeAdapter{caps: []string{}},
		entered:     make(chan struct{}),
		gate:        make(chan struct{}),
	}
}

// readUntilType reads controller-side frames until one of the wanted type
// arrives, returning every frame type seen on the way.
func readUntilType(ctrl *websocket.Conn, want string) (protocol.Envelope, error) {
	for {
		if err := ctrl.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
			return protocol.Envelope{}, err
		}
		var env protocol.Envelope
		if err := ctrl.ReadJSON(&env); err != nil {
			return protocol.Envelope{}, fmt.Errorf("waiting for %s: %w", want, err)
		}
		if env.Type == want {
			return env, nil
		}
	}
}

func syncTestConfig(url string) *config.Config {
	return &config.Config{
		DrydockURL:        url,
		HeartbeatInterval: 30,
		WelcomeTimeout:    5,
		DDPollInterval:    300,
		SkipDFCollection:  true,
	}
}

// A blocked inventory sync must not stop the connection serving pings and API
// requests, and the inventory still goes out once the sync finishes.
func TestConnectServesRequestsWhileInventorySyncs(t *testing.T) {
	t.Parallel()

	ad := newSyncAdapter()
	result := make(chan error, 1)
	url := newControllerServer(t, func(ctrl *websocket.Conn) {
		result <- func() error {
			readAndAckHello(t, ctrl)
			sendWelcomeMsg(t, ctrl, protocol.WelcomeMessage{})
			select {
			case <-ad.entered:
			case <-time.After(readTimeout):
				return fmt.Errorf("OnConnect never started")
			}

			// The inventory sync is blocked here. Everything below must work.
			ping, _ := json.Marshal(protocol.PingMessage{Timestamp: 7})
			if err := ctrl.WriteJSON(protocol.Envelope{Type: protocol.TypePing, Data: ping}); err != nil {
				return err
			}
			if _, err := readUntilType(ctrl, protocol.TypePong); err != nil {
				return fmt.Errorf("ping not served during sync: %w", err)
			}
			req, _ := json.Marshal(protocol.RequestMessage{RequestID: "r1", Method: http.MethodGet, Path: "/_ping"})
			if err := ctrl.WriteJSON(protocol.Envelope{Type: protocol.TypeRequest, Data: req}); err != nil {
				return err
			}
			env, err := readUntilType(ctrl, protocol.TypeResponse)
			if err != nil {
				return fmt.Errorf("request not served during sync: %w", err)
			}
			var resp protocol.ResponseMessage
			if err := json.Unmarshal(env.Data, &resp); err != nil || resp.RequestID != "r1" || resp.StatusCode != http.StatusOK {
				return fmt.Errorf("response = %+v (err %w)", resp, err)
			}
			if ad.finished.Load() {
				return fmt.Errorf("sync finished before it was released; the test proved nothing")
			}

			close(ad.gate)
			if _, err := readUntilType(ctrl, protocol.TypeDDContainerSync); err != nil {
				return fmt.Errorf("inventory not sent after release: %w", err)
			}
			return nil
		}()
	})

	c := newWireClient(t, syncTestConfig(url))
	c.adapter = ad
	//nolint:bodyclose // the response body is consumed and closed by handleRequest, the code under test.
	c.dockerClient = &fakeDocker{doResp: mkResp(http.StatusOK, "application/json", `{"ok":true}`)}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	established, _ := c.connect(ctx)
	if !established {
		t.Fatal("connection was not established")
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

// A connection that ends while the inventory sync is blocked cancels and joins
// the sync before connect returns, and the late send never reaches the next
// connection.
func TestConnectDropDuringInventorySyncCancelsItAndSendsNothingLater(t *testing.T) {
	t.Parallel()

	ad := newSyncAdapter()
	dropped := make(chan struct{})
	url1 := newControllerServer(t, func(ctrl *websocket.Conn) {
		readAndAckHello(t, ctrl)
		sendWelcomeMsg(t, ctrl, protocol.WelcomeMessage{})
		select {
		case <-ad.entered:
		case <-time.After(readTimeout):
			t.Error("OnConnect never started")
		}
		close(dropped)
	})

	c := newWireClient(t, syncTestConfig(url1))
	c.adapter = ad

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	established, _ := c.connect(ctx)
	<-dropped
	if !established {
		t.Fatal("connection was not established")
	}
	if !ad.finished.Load() {
		t.Fatal("connect returned while the inventory sync was still running")
	}
	if err, _ := ad.ctxErr.Load().(error); err == nil {
		t.Fatal("inventory sync ended without its context being canceled")
	}

	var mu sync.Mutex
	var seen []string
	secondDone := make(chan error, 1)
	url2 := newControllerServer(t, func(ctrl *websocket.Conn) {
		secondDone <- func() error {
			readAndAckHello(t, ctrl)
			sendWelcomeMsg(t, ctrl, protocol.WelcomeMessage{})
			ping, _ := json.Marshal(protocol.PingMessage{Timestamp: 9})
			if err := ctrl.WriteJSON(protocol.Envelope{Type: protocol.TypePing, Data: ping}); err != nil {
				return err
			}
			deadline := time.Now().Add(300 * time.Millisecond)
			gotPong := false
			for time.Now().Before(deadline) {
				_ = ctrl.SetReadDeadline(deadline)
				var env protocol.Envelope
				if err := ctrl.ReadJSON(&env); err != nil {
					break
				}
				mu.Lock()
				seen = append(seen, env.Type)
				mu.Unlock()
				if env.Type == protocol.TypePong {
					gotPong = true
				}
			}
			if !gotPong {
				return fmt.Errorf("second connection never answered its ping")
			}
			return nil
		}()
	})
	c.cfg.DrydockURL = url2
	established, _ = c.connect(ctx)
	if !established {
		t.Fatal("second connection was not established")
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, typ := range seen {
		if typ == protocol.TypeDDContainerSync {
			t.Fatalf("stale inventory reached the next connection: frames %v", seen)
		}
	}
}
