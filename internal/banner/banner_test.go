package banner

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderStripsColorWhenNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	var buf bytes.Buffer
	Render(&buf, Info{Version: "9.9.9", Mode: "edge", Adapter: "drydock"})
	out := buf.String()

	if strings.Contains(out, "\x1b[") {
		t.Fatalf("expected no ANSI escapes when NO_COLOR is set, got escapes in output")
	}
	if !strings.Contains(out, "portwing 9.9.9") {
		t.Fatalf("expected version summary in output, got:\n%s", out)
	}
	if !strings.Contains(out, "edge mode") || !strings.Contains(out, "drydock adapter") {
		t.Fatalf("expected mode and adapter in summary, got:\n%s", out)
	}
}

func TestRenderEmitsColorWhenForced(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "1")

	var buf bytes.Buffer
	Render(&buf, Info{Version: "1.0.0"})

	if !strings.Contains(buf.String(), "\x1b[38;2;") {
		t.Fatal("expected truecolor ANSI escapes when FORCE_COLOR is set")
	}
}

func TestArtEmbedded(t *testing.T) {
	if strings.TrimSpace(art) == "" {
		t.Fatal("embedded portwing.ans is empty — run `go generate ./internal/banner/...`")
	}
}

func TestCenterArtNoOpWhenNarrow(t *testing.T) {
	block := "abc\ndef\n"
	if got := centerArt(block, 0); got != block {
		t.Fatalf("expected unchanged block for cols=0, got %q", got)
	}
	if got := centerArt(block, 2); got != block {
		t.Fatalf("expected unchanged block when terminal narrower than art, got %q", got)
	}
}
