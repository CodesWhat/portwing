package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

// Tests for the container_logs output bounds.

// rawLogLines joins lines into a raw (TTY) log stream, newline-terminated.
func rawLogLines(lines []string) *bytes.Reader {
	return bytes.NewReader([]byte(strings.Join(lines, "\n") + "\n"))
}

func numberedLines(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i)
	}
	return lines
}

func prefixed(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = "stdout: " + line
	}
	return out
}

func TestLogTailLineCap(t *testing.T) {
	tests := []struct {
		name          string
		in            int
		wantTruncated bool
	}{
		{name: "exactly the cap", in: maxLogLines, wantTruncated: false},
		{name: "one over the cap", in: maxLogLines + 1, wantTruncated: true},
		{name: "ring wraps more than twice", in: 2*maxLogLines + 237, wantTruncated: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			all := numberedLines(tt.in)
			lines, truncated, err := decodeContainerLogTail(rawLogLines(all))
			if err != nil {
				t.Fatal(err)
			}
			want := prefixed(all[max(0, tt.in-maxLogLines):])
			if strings.Join(lines, "\n") != strings.Join(want, "\n") {
				t.Errorf("kept %d lines, first %q, want the newest %d starting %q", len(lines), lines[0], len(want), want[0])
			}
			if truncated != tt.wantTruncated {
				t.Errorf("truncated = %v, want %v", truncated, tt.wantTruncated)
			}
		})
	}
}

func TestLogTailByteBudget(t *testing.T) {
	// Four lines of exactly a quarter of the budget each, prefix included.
	quarter := strings.Repeat("q", maxLogBytes/4-len("stdout: "))
	fits := []string{quarter + "0", quarter + "1", quarter + "2", quarter + "3"}
	for i := range fits {
		fits[i] = fits[i][1:] // keep each line at exactly maxLogBytes/4 with its prefix
	}

	t.Run("exactly the budget", func(t *testing.T) {
		lines, truncated, err := decodeContainerLogTail(rawLogLines(fits))
		if err != nil {
			t.Fatal(err)
		}
		if len(lines) != 4 || truncated {
			t.Errorf("kept %d lines, truncated %v; want all 4, not truncated", len(lines), truncated)
		}
		if total := len(strings.Join(lines, "")); total != maxLogBytes {
			t.Errorf("total = %d bytes, want %d", total, maxLogBytes)
		}
	})

	t.Run("one byte over drops the oldest line", func(t *testing.T) {
		over := append([]string{"a"}, fits...)
		lines, truncated, err := decodeContainerLogTail(rawLogLines(over))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(lines, "\n") != strings.Join(prefixed(fits), "\n") || !truncated {
			t.Errorf("kept %d lines, truncated %v; want the 4 newest, truncated", len(lines), truncated)
		}
	})

	t.Run("a large newest line evicts several old ones", func(t *testing.T) {
		big := strings.Repeat("b", maxLogBytes/2-len("stdout: "))
		lines, truncated, err := decodeContainerLogTail(rawLogLines(append(append([]string{}, fits...), big)))
		if err != nil {
			t.Fatal(err)
		}
		want := prefixed([]string{fits[2], fits[3], big})
		if strings.Join(lines, "\n") != strings.Join(want, "\n") || !truncated {
			t.Errorf("kept %d lines, truncated %v; want the last two quarters and the big line", len(lines), truncated)
		}
	})
}

func TestLogTailLongLine(t *testing.T) {
	tests := []struct {
		name          string
		line          string
		want          string
		wantTruncated bool
	}{
		{
			name: "exactly the line limit",
			line: strings.Repeat("x", maxLogLineBytes),
			want: strings.Repeat("x", maxLogLineBytes),
		},
		{
			name:          "one byte over",
			line:          strings.Repeat("x", maxLogLineBytes+1),
			want:          strings.Repeat("x", maxLogLineBytes),
			wantTruncated: true,
		},
		{
			name:          "far over the whole budget",
			line:          strings.Repeat("y", 4*maxLogBytes),
			want:          strings.Repeat("y", maxLogLineBytes),
			wantTruncated: true,
		},
		{
			name:          "cut lands inside a four-byte rune",
			line:          strings.Repeat("x", maxLogLineBytes-2) + "😀" + "tail",
			want:          strings.Repeat("x", maxLogLineBytes-2),
			wantTruncated: true,
		},
		{
			name:          "cut lands just after a rune",
			line:          strings.Repeat("x", maxLogLineBytes-4) + "😀" + "tail",
			want:          strings.Repeat("x", maxLogLineBytes-4) + "😀",
			wantTruncated: true,
		},
		{
			name:          "no rune start before the cut",
			line:          strings.Repeat("\x80", maxLogLineBytes+5),
			want:          "",
			wantTruncated: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Feed it in small fragments so the line spans many payloads.
			reader := &fragmentedLogReader{reader: bytes.NewReader([]byte(tt.line + "\n")), max: 4093}
			lines, truncated, err := decodeContainerLogTail(reader)
			if err != nil {
				t.Fatal(err)
			}
			if len(lines) != 1 || lines[0] != "stdout: "+tt.want {
				t.Fatalf("got %d lines (first %d bytes), want one line of %d bytes", len(lines), len(lines[0]), len(tt.want)+8)
			}
			if truncated != tt.wantTruncated {
				t.Errorf("truncated = %v, want %v", truncated, tt.wantTruncated)
			}
			if utf8.ValidString(tt.line) && !utf8.ValidString(lines[0]) {
				t.Error("cut produced invalid UTF-8 from a valid line")
			}
		})
	}
}

func TestLogTailLongLineThenShortLines(t *testing.T) {
	long := strings.Repeat("z", maxLogLineBytes+10)
	lines, truncated, err := decodeContainerLogTail(rawLogLines([]string{long, "after"}))
	if err != nil {
		t.Fatal(err)
	}
	// The cut long line plus "after" exceed the budget, so only "after" stays.
	if len(lines) != 1 || lines[0] != "stdout: after" || !truncated {
		t.Errorf("lines = %d (last %q), truncated %v", len(lines), lines[len(lines)-1], truncated)
	}
}

func TestLogTailMultiplexedStreamsKeepOrderAndPrefixes(t *testing.T) {
	var stream []byte
	for i := range maxLogLines + 2 {
		kind := byte(1 + i%2)
		stream = append(stream, mcpTestLogFrame(kind, fmt.Appendf(nil, "l%d\n", i))...)
	}
	lines, truncated, err := decodeContainerLogTail(bytes.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != maxLogLines || !truncated {
		t.Fatalf("kept %d lines, truncated %v", len(lines), truncated)
	}
	if lines[0] != "stdout: l2" || lines[len(lines)-1] != fmt.Sprintf("stderr: l%d", maxLogLines+1) {
		t.Errorf("first %q last %q", lines[0], lines[len(lines)-1])
	}
}

func TestLogTailEmptyAndSmall(t *testing.T) {
	lines, truncated, err := decodeContainerLogTail(bytes.NewReader(nil))
	if err != nil || lines != nil || truncated {
		t.Errorf("empty stream = (%v, %v, %v), want (nil, false, nil)", lines, truncated, err)
	}
	lines, truncated, err = decodeContainerLogTail(bytes.NewReader([]byte("a\n\nb")))
	if err != nil || truncated || strings.Join(lines, "|") != "stdout: a|stdout: |stdout: b" {
		t.Errorf("small stream = (%q, %v, %v)", lines, truncated, err)
	}
}

func TestToolContainerLogsReportsTruncation(t *testing.T) {
	tests := []struct {
		name          string
		lines         int
		wantLines     int
		wantTruncated bool
	}{
		{name: "within bounds", lines: 3, wantLines: 3, wantTruncated: false},
		{name: "daemon sends more than the cap", lines: maxLogLines + 50, wantLines: maxLogLines, wantTruncated: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, shutdown := stubDockerWithLogs(t, []byte(strings.Join(numberedLines(tt.lines), "\n")+"\n"))
			defer shutdown()
			rr := postMCP(t, NewHandler(client, nil), map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"method":  "tools/call",
				"params":  map[string]any{"name": "container_logs", "arguments": map[string]any{"id": "abc123", "tail": 500}},
			})
			if rr.Code != http.StatusOK {
				t.Fatalf("status %d", rr.Code)
			}
			text := extractTextContent(t, extractToolResult(t, decodeResponse(t, rr)))
			var out struct {
				ID        string   `json:"id"`
				Lines     []string `json:"lines"`
				Truncated *bool    `json:"truncated"`
			}
			if err := json.Unmarshal([]byte(text), &out); err != nil {
				t.Fatalf("decode %s: %v", text, err)
			}
			if len(out.Lines) != tt.wantLines {
				t.Errorf("lines = %d, want %d", len(out.Lines), tt.wantLines)
			}
			if out.Truncated == nil || *out.Truncated != tt.wantTruncated {
				t.Errorf("truncated = %v, want %v", out.Truncated, tt.wantTruncated)
			}
		})
	}
}

func TestLogBoundConstants(t *testing.T) {
	if maxLogLineBytes+len("stdout: ") != maxLogBytes || len("stderr: ") != len("stdout: ") {
		t.Errorf("maxLogLineBytes = %d, want maxLogBytes (%d) minus the 8-byte stream prefix", maxLogLineBytes, maxLogBytes)
	}
}

// TestLogTailPendingStaysBounded checks the partial-line buffer never holds
// more than one byte past the line limit, however long the line runs.
func TestLogTailPendingStaysBounded(t *testing.T) {
	tail := new(logTail)
	chunk := bytes.Repeat([]byte("p"), 4096)
	for range 3 * maxLogLineBytes / len(chunk) {
		if err := tail.write(1, chunk); err != nil {
			t.Fatal(err)
		}
		if tail.pending.Len() > maxLogLineBytes+1 {
			t.Fatalf("pending grew to %d bytes", tail.pending.Len())
		}
	}
	if tail.pending.Len() != maxLogLineBytes+1 {
		t.Errorf("pending = %d bytes, want %d", tail.pending.Len(), maxLogLineBytes+1)
	}
}
