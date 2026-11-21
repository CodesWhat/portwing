// SPDX-License-Identifier: MIT

package drydock

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/codeswhat/portwing/internal/protocol"
)

// forwardLogFrames runs frames through forwardContainerLogStream and returns
// the text each stream received after the JSON round trip, plus the number of
// chunks sent.
func forwardLogFrames(t *testing.T, body []byte) (map[string]string, int) {
	t.Helper()

	a := &Adapter{}
	sender := &logStreamTestSender{events: make(chan logStreamTestEvent, 256)}
	err := a.forwardContainerLogStream(
		context.Background(),
		sender,
		protocol.DDContainerLogRequestMessage{RequestID: "utf8", ContainerID: "c"},
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("forwardContainerLogStream: %v", err)
	}
	close(sender.events)

	got := map[string]string{}
	count := 0
	for event := range sender.events {
		if event.msgType != protocol.TypeDDContainerLogChunk {
			t.Fatalf("unexpected message type %q", event.msgType)
		}
		var chunk protocol.DDContainerLogChunkMessage
		if err := json.Unmarshal(event.data, &chunk); err != nil {
			t.Fatalf("decode chunk: %v", err)
		}
		got[chunk.Stream] += chunk.Logs
		count++
	}
	return got, count
}

func TestContainerLogStreamCarriesSplitUTF8AcrossChunks(t *testing.T) {
	t.Parallel()

	samples := map[string]string{
		"2-byte": "café üñ\n",
		"3-byte": "€ price 日本語\n",
		"4-byte": "go \U0001F600 \U0001F680 done\n",
	}
	for name, text := range samples {
		raw := []byte(text)
		for _, stream := range []byte{1, 2} {
			streamName := map[byte]string{1: "stdout", 2: "stderr"}[stream]
			for k := 1; k < len(raw); k++ {
				t.Run(fmt.Sprintf("%s/%s/split-%d", name, streamName, k), func(t *testing.T) {
					t.Parallel()
					body := append(routeTestDockerLogFrame(stream, raw[:k]), routeTestDockerLogFrame(stream, raw[k:])...)
					got, _ := forwardLogFrames(t, body)
					if got[streamName] != text {
						t.Fatalf("%s = %q, want %q", streamName, got[streamName], text)
					}
					if strings.ContainsRune(got[streamName], '�') {
						t.Fatalf("%s contains U+FFFD: %q", streamName, got[streamName])
					}
				})
			}
		}
	}
}

func TestContainerLogStreamSixteenByteFirstChunkSplitsCharacter(t *testing.T) {
	t.Parallel()

	// 15 ASCII bytes then a 3-byte character: the first 16-byte chunk ends one
	// byte into the character.
	text := strings.Repeat("a", 15) + "€ tail\n"
	raw := []byte(text)
	body := append(routeTestDockerLogFrame(1, raw[:16]), routeTestDockerLogFrame(1, raw[16:])...)

	got, _ := forwardLogFrames(t, body)
	if got["stdout"] != text {
		t.Fatalf("stdout = %q, want %q", got["stdout"], text)
	}
}

func TestContainerLogStreamUTF8SplitsStayPerStream(t *testing.T) {
	t.Parallel()

	out := []byte("out € done\n")
	errText := []byte("err \U0001F600 done\n")
	// Split each stream mid-character and interleave the halves so a shared
	// buffer would splice stdout's lead bytes onto stderr's continuation.
	outCut := bytes.IndexByte(out, 0xE2) + 1
	errCut := bytes.IndexByte(errText, 0xF0) + 2
	var body []byte
	body = append(body, routeTestDockerLogFrame(1, out[:outCut])...)
	body = append(body, routeTestDockerLogFrame(2, errText[:errCut])...)
	body = append(body, routeTestDockerLogFrame(1, out[outCut:])...)
	body = append(body, routeTestDockerLogFrame(2, errText[errCut:])...)

	got, _ := forwardLogFrames(t, body)
	if got["stdout"] != string(out) {
		t.Fatalf("stdout = %q, want %q", got["stdout"], out)
	}
	if got["stderr"] != string(errText) {
		t.Fatalf("stderr = %q, want %q", got["stderr"], errText)
	}
}

func TestContainerLogStreamFlushesIncompleteFinalCharacter(t *testing.T) {
	t.Parallel()

	// "ok" then the first two bytes of a 3-byte character, and nothing after.
	raw := []byte("ok\xe2\x82")
	got, _ := forwardLogFrames(t, routeTestDockerLogFrame(1, raw))

	if want := "ok\ufffd\ufffd"; got["stdout"] != want {
		t.Fatalf("stdout = %q, want %q (incomplete tail emitted, not dropped)", got["stdout"], want)
	}
}

func TestIncompleteUTF8Suffix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   []byte
		want int
	}{
		{"empty", nil, 0},
		{"ascii", []byte("abc"), 0},
		{"complete 2-byte", []byte("é"), 0},
		{"lead of 2-byte", []byte("a\xc3"), 1},
		{"lead of 3-byte", []byte("a\xe2"), 1},
		{"two of 3-byte", []byte("a\xe2\x82"), 2},
		{"three of 4-byte", []byte("a\xf0\x9f\x98"), 3},
		{"complete 4-byte", []byte("\U0001F600"), 0},
		{"stray continuation", []byte("a\x80\x80\x80"), 0},
		{"invalid lead", []byte("a\xff"), 0},
	}
	for _, tc := range cases {
		if got := incompleteUTF8Suffix(tc.in); got != tc.want {
			t.Errorf("%s: incompleteUTF8Suffix(%q) = %d, want %d", tc.name, tc.in, got, tc.want)
		}
	}
}
