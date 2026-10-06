package edge

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/codeswhat/portwing/internal/audit"
	"github.com/codeswhat/portwing/internal/protocol"
)

// execAdmissionRounds is enough rounds for the unlocked admission to lose a
// race, and few enough to finish well inside a second.
const execAdmissionRounds = 150

// Typed and raw exec starts racing for the last slot are admitted exactly once
// between them. Without execAdmissionMu in admitTypedExec the shared cap is
// read and written unsynchronised, which the race detector reports and which
// can admit two.
func TestExecAdmissionLastSlotAdmitsOnce(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		typed  int
		rawIDs int
	}{
		{"typed against raw", 1, 1},
		{"typed against typed", 2, 0},
		{"two typed against two raw", 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runAdmissionRace(t, tc.typed, tc.rawIDs)
		})
	}
}

func runAdmissionRace(t *testing.T, typed, raw int) {
	t.Helper()

	c, ctrl := newTestClient(t)
	// Drain the controller side so denial replies never back the pump up.
	go func() {
		for {
			if _, _, err := ctrl.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// Hold every winning bring-up inside CreateExec so it keeps its slot for
	// the round and cannot free one for a later racer.
	hold := make(chan struct{})
	var entered sync.WaitGroup
	c.dockerClient = &fakeDocker{createHook: func() {
		entered.Done()
		<-hold
	}}
	for i := 0; i < maxExecSessions-1; i++ {
		c.execSessions.Store(fmt.Sprintf("fill-%d", i), &ExecSession{})
	}

	var winners []*ExecSession
	t.Cleanup(func() {
		close(hold)
		deadline := time.After(readTimeout)
		for _, s := range winners {
			select {
			case <-s.done:
			case <-deadline:
				t.Error("held bring-up never finished")
				return
			}
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	for round := 0; round < execAdmissionRounds; round++ {
		logger, closeAudit, err := audit.New("", 64)
		if err != nil {
			t.Fatal(err)
		}
		c.auditor = logger

		start := make(chan struct{})
		var wg sync.WaitGroup
		var mu sync.Mutex
		admittedRaw := 0
		for i := 0; i < typed; i++ {
			id := fmt.Sprintf("t-%d-%d", round, i)
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				c.StartExec(ctx, protocol.ExecStartMessage{ExecID: id, ContainerID: "c1"})
			}()
		}
		for i := 0; i < raw; i++ {
			path := fmt.Sprintf("/exec/r-%d-%d/start", round, i)
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if _, admitted := c.admitRawExecStart(http.MethodPost, path); admitted {
					mu.Lock()
					admittedRaw++
					mu.Unlock()
				}
			}()
		}
		// A typed winner enters CreateExec exactly once per round.
		entered.Add(1)
		close(start)
		wg.Wait()

		c.execAdmissionMu.Lock()
		held := c.rawExecStarts
		c.execAdmissionMu.Unlock()
		var sessions []*ExecSession
		c.execSessions.Range(func(k, v any) bool {
			if s, ok := v.(*ExecSession); ok && s.client != nil {
				sessions = append(sessions, s)
			}
			held++
			return true
		})
		if held != maxExecSessions {
			t.Fatalf("round %d: %d slots held, want exactly the cap %d", round, held, maxExecSessions)
		}

		records := execRecords(logger)
		allowed, denied := 0, 0
		for _, r := range records {
			switch r.Outcome {
			case audit.OutcomeAllowed:
				allowed++
			case audit.OutcomeDenied:
				denied++
			}
		}
		if allowed != 1 || denied != typed+raw-1 || len(records) != typed+raw {
			t.Fatalf("round %d: records = %+v, want one allowed and %d denied", round, records, typed+raw-1)
		}

		// Reset for the next round. A typed winner still holds CreateExec.
		if admittedRaw == 1 {
			entered.Done()
			c.releaseRawExecSlot()
		} else {
			entered.Wait()
			winners = append(winners, sessions...)
			for _, s := range sessions {
				c.execSessions.Delete(s.execID)
			}
		}
		closeAudit()
	}
}
