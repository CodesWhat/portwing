//go:build !windows

package banner

import (
	"os"
	"syscall"
	"testing"
)

// TestTerminalColsSuccessPath exercises the return int(ws.Col) branch by
// replacing ioctlGetWinsize with a stub that succeeds and writes a non-zero
// column count. We use /dev/null as the writer because it is a char device,
// which is required to pass the fi.Mode()&os.ModeCharDevice check.
func TestTerminalColsSuccessPath(t *testing.T) {
	f, err := os.Open("/dev/null")
	if err != nil {
		t.Skipf("cannot open /dev/null: %v", err)
	}
	defer f.Close()

	// Replace ioctlGetWinsize with a stub that reports 120 columns and no error.
	orig := ioctlGetWinsize
	t.Cleanup(func() { ioctlGetWinsize = orig })
	ioctlGetWinsize = func(fd uintptr, ws *winsize) syscall.Errno {
		ws.Col = 120
		return 0
	}

	got := terminalCols(f)
	if got != 120 {
		t.Fatalf("terminalCols with mocked ioctl: got %d, want 120", got)
	}
}
