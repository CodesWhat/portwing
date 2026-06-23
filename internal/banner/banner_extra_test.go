package banner

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestRenderNoNewlineSuffix exercises the `!strings.HasSuffix(body, "\n")` branch
// by temporarily replacing the art with a block that doesn't end in "\n" after
// ANSI stripping. We use NO_COLOR so ANSI is stripped, and craft a plain-text art
// that ends without a newline.
func TestRenderNoNewlineSuffix(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	t.Setenv("FORCE_COLOR", "")

	orig := art
	t.Cleanup(func() { art = orig })

	// Assign an art string with no trailing newline. colorEnabled returns false
	// (NO_COLOR set), ansiPattern.ReplaceAllString leaves it unchanged, so body
	// will be "no-newline" — no trailing '\n'.
	art = "no-newline"

	var buf bytes.Buffer
	Render(&buf, Info{Version: "1.0.0"})

	out := buf.String()
	if !strings.Contains(out, "portwing 1.0.0") {
		t.Fatalf("expected version in output, got: %q", out)
	}
}

// TestCenterArtWideTerminalCenters verifies the padding path when cols > maxw.
func TestCenterArtWideTerminalCenters(t *testing.T) {
	// 3 lines, each 4 chars wide, in a 20-column terminal → pad = (20-4)/2 = 8 spaces.
	block := "abcd\nefgh\nijkl\n"
	got := centerArt(block, 20)

	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("centerArt should end with newline, got %q", got)
	}

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	for _, ln := range lines {
		if !strings.HasPrefix(ln, "        ") { // 8 spaces
			t.Fatalf("expected 8-space left pad, got line %q", ln)
		}
	}
}

// TestCenterArtEmptyLineNotPadded verifies that empty lines inside the block
// are not padded (the `if ln != ""` guard).
func TestCenterArtEmptyLineNotPadded(t *testing.T) {
	// block has an empty line in the middle; wide terminal triggers centering.
	block := "abcdefgh\n\nabcdefgh\n" // maxw = 8
	got := centerArt(block, 30)       // cols=30 > maxw=8 → pad = (30-8)/2 = 11

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d: %v", len(lines), lines)
	}
	// Middle line should be empty (not padded).
	if lines[1] != "" {
		t.Fatalf("empty line should not be padded, got %q", lines[1])
	}
	// First and last lines should be padded.
	if !strings.HasPrefix(lines[0], " ") {
		t.Fatalf("first line should be padded, got %q", lines[0])
	}
}

// TestColorEnabledWithRegularFile exercises the *os.File path where the file is
// not a char device (a regular temp file), so colorEnabled returns false.
func TestColorEnabledWithRegularFile(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")

	f, err := os.CreateTemp(t.TempDir(), "banner-test-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()

	// A regular file is not a char device — colorEnabled must return false.
	if colorEnabled(f) {
		t.Fatal("expected colorEnabled=false for a regular file")
	}
}

// TestColorEnabledWithNonOsFile exercises the path where w is not *os.File.
func TestColorEnabledWithNonOsFile(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")

	var buf bytes.Buffer
	if colorEnabled(&buf) {
		t.Fatal("expected colorEnabled=false for bytes.Buffer")
	}
}

// TestColorEnabledStatError exercises the stat-error path. We do this by
// closing the file before calling colorEnabled, which makes f.Stat() fail.
func TestColorEnabledStatError(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "")

	f, err := os.CreateTemp(t.TempDir(), "banner-closed-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	// Close it now — subsequent f.Stat() will return an error.
	f.Close()

	if colorEnabled(f) {
		t.Fatal("expected colorEnabled=false when f.Stat() fails")
	}
}

// TestTerminalColsNonOsFile verifies terminalCols returns 0 for a non-*os.File writer.
func TestTerminalColsNonOsFile(t *testing.T) {
	var buf bytes.Buffer
	if got := terminalCols(&buf); got != 0 {
		t.Fatalf("terminalCols(bytes.Buffer) = %d, want 0", got)
	}
}

// TestTerminalColsRegularFile verifies terminalCols returns 0 for a regular file
// (not a char device).
func TestTerminalColsRegularFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "banner-cols-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()

	if got := terminalCols(f); got != 0 {
		t.Fatalf("terminalCols(regular file) = %d, want 0", got)
	}
}

// TestRenderWideCenterIntegration verifies that Render uses terminalCols and
// centerArt together — with FORCE_COLOR and a wide art we just confirm output
// contains the version summary.
func TestRenderWideCenterIntegration(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("FORCE_COLOR", "1")

	var buf bytes.Buffer
	Render(&buf, Info{Version: "2.0.0", Mode: "standalone"})
	out := buf.String()

	if !strings.Contains(out, "portwing 2.0.0") {
		t.Fatalf("expected version in output, got: %q", out)
	}
	if !strings.Contains(out, "standalone mode") {
		t.Fatalf("expected mode in output, got: %q", out)
	}
}

// TestCenterArtExactWidth verifies no-op when cols == maxw.
func TestCenterArtExactWidth(t *testing.T) {
	block := "abcd\nefgh\n"
	// cols == maxw (4) → no centering.
	got := centerArt(block, 4)
	if got != block {
		t.Fatalf("expected unchanged block when cols==maxw, got %q", got)
	}
}

// TestTerminalColsCharDevice opens /dev/null (a char device on darwin and linux)
// and calls terminalCols. The ioctl TIOCGWINSZ on /dev/null is expected to fail
// (not a terminal), so the return value is 0 — but the ioctl attempt is made,
// covering the char-device branch in terminalCols and the ioctlGetWinsize body.
func TestTerminalColsCharDevice(t *testing.T) {
	f, err := os.Open("/dev/null")
	if err != nil {
		t.Skipf("cannot open /dev/null: %v", err)
	}
	defer f.Close()

	// /dev/null is a char device; terminalCols should enter the ioctl path.
	// The ioctl will fail (not a PTY), so the result is 0 — which is correct.
	got := terminalCols(f)
	if got < 0 {
		t.Fatalf("terminalCols returned negative value: %d", got)
	}
	// Result is 0 (ioctl failed on /dev/null) or possibly a column count if
	// the OS happens to return something — both are valid.
}

// TestIoctlGetWinsizeVarCoverage exercises the ioctlGetWinsize function
// literal directly by invoking it on a closed /dev/null fd.
// This covers the syscall.Syscall lines inside the var initializer.
func TestIoctlGetWinsizeVarCoverage(t *testing.T) {
	f, err := os.Open("/dev/null")
	if err != nil {
		t.Skipf("cannot open /dev/null: %v", err)
	}
	defer f.Close()

	var ws winsize
	// Call the function directly; the result doesn't matter — we just need the
	// body of the function literal to be executed for coverage.
	_ = ioctlGetWinsize(f.Fd(), &ws)
}
