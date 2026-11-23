// SPDX-License-Identifier: MIT

package edge

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/codeswhat/portwing/internal/protocol"
)

const reassemblyChunk = 3<<20 + 1 // deliberately not a power of two

// shrinkBodyBudgets sets the per-request and aggregate reassembly limits for
// one test and restores them afterwards. Callers must not be parallel: the
// limits are package-level vars.
func shrinkBodyBudgets(t *testing.T, perRequest, aggregate int64) {
	t.Helper()
	origPer, origAgg := maxRequestBodyStream, maxStreamedRequestBodyBytes
	maxRequestBodyStream, maxStreamedRequestBodyBytes = perRequest, aggregate
	t.Cleanup(func() { maxRequestBodyStream, maxStreamedRequestBodyBytes = origPer, origAgg })
}

func encodedChunk(n int) string {
	return base64.StdEncoding.EncodeToString(make([]byte, n))
}

func pendingBody(c *Client, id string) *pendingRequestBody {
	c.pendingBodiesMu.Lock()
	defer c.pendingBodiesMu.Unlock()
	return c.pendingBodies[id]
}

func chargedBytes(c *Client) int64 {
	c.pendingBodiesMu.Lock()
	defer c.pendingBodiesMu.Unlock()
	return c.streamedBodyBytesLocked()
}

// The budget is charged for the backing array, so a chunk sequence that is not
// a power of two still totals within the budget and the charge equals cap().
func TestPendingBodyChargeEqualsBackingCapacity(t *testing.T) {
	shrinkBodyBudgets(t, 64<<20, 128<<20)
	c, _ := newTestClient(t)
	c.registerPendingBody(protocol.RequestMessage{RequestID: "up", BodyStream: true}, c.currentOutboundTarget())
	chunk := encodedChunk(reassemblyChunk)

	for i := 1; i <= 12; i++ {
		if !c.appendPendingBody("up", chunk) {
			t.Fatalf("chunk %d was not consumed", i)
		}
		pb := pendingBody(c, "up")
		if pb == nil {
			t.Fatalf("upload rejected at chunk %d", i)
		}
		if got := chargedBytes(c); got != int64(cap(pb.buf)) {
			t.Fatalf("chunk %d: charged %d, cap %d", i, got, cap(pb.buf))
		}
		if len(pb.buf) != i*reassemblyChunk {
			t.Fatalf("chunk %d: len %d, want %d", i, len(pb.buf), i*reassemblyChunk)
		}
		if int64(cap(pb.buf)) > maxStreamedRequestBodyBytes {
			t.Fatalf("chunk %d: cap %d exceeds the budget %d", i, cap(pb.buf), maxStreamedRequestBodyBytes)
		}
		// Quarter growth, not doubling: no more than 1.5x the bytes held.
		if cap(pb.buf) > len(pb.buf)*3/2 {
			t.Fatalf("chunk %d: cap %d is over 1.5x len %d", i, cap(pb.buf), len(pb.buf))
		}
	}
}

// Growth that would pass the budget is refused before anything is allocated:
// the buffer never exceeds the budget and the upload is failed.
func TestPendingBodyGrowthPastBudgetIsRejectedBeforeAllocating(t *testing.T) {
	const budget = 10 << 20
	shrinkBodyBudgets(t, 64<<20, budget)
	c, ctrl := newTestClient(t)
	c.registerPendingBody(protocol.RequestMessage{RequestID: "big", BodyStream: true}, c.currentOutboundTarget())
	pb := pendingBody(c, "big")
	chunk := encodedChunk(reassemblyChunk)

	// The old and new arrays coexist during a growth copy, so the third chunk
	// (9 MiB of data beside a 6 MiB array) already has to be refused.
	var prevLen, prevCap int
	for i := 0; i < 4 && pendingBody(c, "big") != nil; i++ {
		prevLen, prevCap = len(pb.buf), cap(pb.buf)
		c.appendPendingBody("big", chunk)
		if cap(pb.buf) > budget {
			t.Fatalf("chunk %d: cap %d passed the %d budget", i+1, cap(pb.buf), budget)
		}
	}
	if pendingBody(c, "big") != nil {
		t.Fatal("upload past the budget was never rejected")
	}
	if len(pb.buf) != prevLen || cap(pb.buf) != prevCap || prevLen == 0 {
		t.Fatalf("rejected chunk changed the buffer: len %d cap %d, was len %d cap %d", len(pb.buf), cap(pb.buf), prevLen, prevCap)
	}
	if pendingBody(c, "big") != nil {
		t.Fatal("rejected upload is still pending")
	}
	var em protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &em)
	if em.RequestID != "big" || !strings.Contains(em.Message, "aggregate in-memory limit") {
		t.Fatalf("error = %+v, want the aggregate limit for big", em)
	}
	if got := chargedBytes(c); got != 0 {
		t.Fatalf("charged %d after rejection, want 0", got)
	}
}

// A first chunk that can never fit allocates nothing.
func TestPendingBodyFirstChunkOverBudgetAllocatesNothing(t *testing.T) {
	shrinkBodyBudgets(t, 64<<20, 1<<20)
	c, ctrl := newTestClient(t)
	c.registerPendingBody(protocol.RequestMessage{RequestID: "huge", BodyStream: true}, c.currentOutboundTarget())
	pb := pendingBody(c, "huge")

	c.appendPendingBody("huge", encodedChunk(reassemblyChunk))

	if pb.buf != nil {
		t.Fatalf("buffer allocated (cap %d) for a chunk over the budget", cap(pb.buf))
	}
	expectType(t, ctrl, protocol.TypeError)
}

// The per-request cap applies to bytes held, whatever the capacity.
func TestPendingBodyPerRequestLimit(t *testing.T) {
	shrinkBodyBudgets(t, 4<<20, 64<<20)
	c, ctrl := newTestClient(t)
	c.registerPendingBody(protocol.RequestMessage{RequestID: "cap", BodyStream: true}, c.currentOutboundTarget())
	pb := pendingBody(c, "cap")
	chunk := encodedChunk(reassemblyChunk)

	c.appendPendingBody("cap", chunk)
	c.appendPendingBody("cap", chunk)

	if int64(cap(pb.buf)) > maxRequestBodyStream {
		t.Fatalf("cap %d passed the per-request limit", cap(pb.buf))
	}
	var em protocol.ErrorMessage
	decodeData(t, expectType(t, ctrl, protocol.TypeError), &em)
	if !strings.Contains(em.Message, "byte limit") {
		t.Fatalf("message = %q, want the per-request limit", em.Message)
	}
}

// With the size declared up front the upload is assembled in one array.
func TestPendingBodyDeclaredSizeAllocatesOnce(t *testing.T) {
	shrinkBodyBudgets(t, 64<<20, 128<<20)
	c, _ := newTestClient(t)
	const total = 5 << 20
	c.registerPendingBody(protocol.RequestMessage{
		RequestID:  "decl",
		BodyStream: true,
		Headers:    map[string]string{"content-length": fmt.Sprint(total)},
	}, c.currentOutboundTarget())
	pb := pendingBody(c, "decl")
	chunk := encodedChunk(1 << 20)

	c.appendPendingBody("decl", chunk)
	first := &pb.buf[0]
	if cap(pb.buf) != total {
		t.Fatalf("cap after first chunk = %d, want the declared %d", cap(pb.buf), total)
	}
	for i := 1; i < 5; i++ {
		c.appendPendingBody("decl", chunk)
	}
	if &pb.buf[0] != first || cap(pb.buf) != total || len(pb.buf) != total {
		t.Fatalf("buffer regrew: len %d cap %d, array moved %v", len(pb.buf), cap(pb.buf), &pb.buf[0] != first)
	}
}

// A declared size that lies low still grows under the budget; one that lies
// high cannot reserve more than the budget holds.
func TestPendingBodyDeclaredSizeIsOnlyAHint(t *testing.T) {
	shrinkBodyBudgets(t, 64<<20, 8<<20)
	c, _ := newTestClient(t)
	c.registerPendingBody(protocol.RequestMessage{
		RequestID: "low", BodyStream: true, Headers: map[string]string{"Content-Length": "10"},
	}, c.currentOutboundTarget())
	c.appendPendingBody("low", encodedChunk(1<<20))
	if pb := pendingBody(c, "low"); pb == nil || len(pb.buf) != 1<<20 {
		t.Fatal("under-declared upload did not grow")
	}

	c.registerPendingBody(protocol.RequestMessage{
		RequestID: "high", BodyStream: true, Headers: map[string]string{"Content-Length": fmt.Sprint(60 << 20)},
	}, c.currentOutboundTarget())
	c.appendPendingBody("high", encodedChunk(1<<20))
	pb := pendingBody(c, "high")
	if pb == nil {
		t.Fatal("over-declared upload was rejected though its bytes fit")
	}
	if got := chargedBytes(c); got > maxStreamedRequestBodyBytes {
		t.Fatalf("charged %d, over the %d budget", got, maxStreamedRequestBodyBytes)
	}
}

// Finishing an upload hands the assembled array over: no copy, and the
// dispatch reservation is the capacity still held.
func TestFinishPendingBodyHandsOffWithoutCopying(t *testing.T) {
	shrinkBodyBudgets(t, 64<<20, 128<<20)
	c, _ := newTestClient(t)
	c.registerPendingBody(protocol.RequestMessage{RequestID: "hand", BodyStream: true}, c.currentOutboundTarget())
	pb := pendingBody(c, "hand")
	c.appendPendingBody("hand", encodedChunk(reassemblyChunk))
	arr := &pb.buf[0]
	held := int64(cap(pb.buf))

	req, _, reservation, ok := c.finishPendingBody("hand")
	if !ok {
		t.Fatal("finishPendingBody found nothing")
	}
	if &req.Body[0] != arr {
		t.Fatal("request body was copied out of the reassembly buffer")
	}
	if len(req.Body) != reassemblyChunk {
		t.Fatalf("body len = %d, want %d", len(req.Body), reassemblyChunk)
	}
	if got := chargedBytes(c); got != held {
		t.Fatalf("dispatch charge = %d, want the backing capacity %d", got, held)
	}
	c.releaseDispatchingBody(reservation)
	if got := chargedBytes(c); got != 0 {
		t.Fatalf("charge after release = %d", got)
	}
}

// An empty upload finishes with a nil body.
func TestFinishPendingBodyEmptyUpload(t *testing.T) {
	c, _ := newTestClient(t)
	c.registerPendingBody(protocol.RequestMessage{RequestID: "none", BodyStream: true}, c.currentOutboundTarget())
	req, _, reservation, ok := c.finishPendingBody("none")
	if !ok || req.Body != nil {
		t.Fatalf("ok=%v body=%v, want a nil body", ok, req.Body)
	}
	c.releaseDispatchingBody(reservation)
}

// Concurrent uploads never push the charge past the aggregate budget.
func TestPendingBodyConcurrentUploadsStayWithinBudget(t *testing.T) {
	const budget = 24 << 20
	shrinkBodyBudgets(t, 16<<20, budget)
	c, _ := newTestClient(t)
	chunk := encodedChunk(reassemblyChunk)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("c%d", i)
		c.registerPendingBody(protocol.RequestMessage{RequestID: id, BodyStream: true}, c.currentOutboundTarget())
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				c.appendPendingBody(id, chunk)
				if got := chargedBytes(c); got > budget {
					t.Errorf("charged %d, over the %d budget", got, budget)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestDeclaredBodySize(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		headers map[string]string
		want    int64
	}{
		{"none", nil, 0},
		{"other header", map[string]string{"X-Foo": "5"}, 0},
		{"canonical", map[string]string{"Content-Length": "42"}, 42},
		{"lower case", map[string]string{"content-length": " 7 "}, 7},
		{"not a number", map[string]string{"Content-Length": "abc"}, 0},
		{"negative", map[string]string{"Content-Length": "-4"}, 0},
	} {
		if got := declaredBodySize(tc.headers); got != tc.want {
			t.Errorf("%s: declaredBodySize = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestNextBodyCap(t *testing.T) {
	shrinkBodyBudgets(t, 64<<20, 128<<20)
	for _, tc := range []struct {
		name         string
		oldCap, need int
		declared     int64
		want         int
	}{
		{"declared first allocation", 0, 10, 1000, 1000},
		{"declared smaller than need", 0, 2000, 1000, 2000},
		{"declared over the limit is ignored", 0, 10, 100 << 20, 4096},
		{"small buffers double", 8192, 8193, 0, 16384},
		{"floor", 0, 10, 0, 4096},
		{"large buffers grow by a quarter", 4 << 20, 4<<20 + 1, 0, 5 << 20},
		{"need beats growth", 4 << 20, 8 << 20, 0, 8 << 20},
	} {
		if got := nextBodyCap(tc.oldCap, tc.need, tc.declared); got != tc.want {
			t.Errorf("%s: nextBodyCap = %d, want %d", tc.name, got, tc.want)
		}
	}
}
