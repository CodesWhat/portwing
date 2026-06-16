//go:build windows

package banner

import "io"

// terminalCols on Windows is a no-op. portwing ships as a Linux container and
// runtime TTY probing via ioctl is POSIX-specific. Returning 0 falls back to
// the left-aligned art, which still renders correctly in any terminal.
func terminalCols(w io.Writer) int { return 0 }
