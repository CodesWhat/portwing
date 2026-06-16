// Package banner renders the portwing startup banner: a truecolor
// half-block render of the logo, generated from portwing.png by the gen
// command and embedded here as portwing.ans.
package banner

import (
	_ "embed"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

//go:generate go run ./gen

//go:embed portwing.ans
var art string

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// Info is the one-line runtime summary rendered beneath the logo.
type Info struct {
	Version string
	Mode    string
	Adapter string
}

// Render writes the logo banner and a summary line to w.
//
// ANSI color is emitted only when color is enabled for w — a TTY, or when
// FORCE_COLOR is set, and never when NO_COLOR is set (https://no-color.org).
// When color is off the escapes are stripped so the silhouette still prints
// cleanly into log files and pipes.
func Render(w io.Writer, info Info) {
	body := art
	if !colorEnabled(w) {
		body = ansiPattern.ReplaceAllString(body, "")
	}
	body = centerArt(body, terminalCols(w))

	var b strings.Builder
	b.WriteByte('\n')
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	line := "  portwing " + info.Version
	if info.Mode != "" {
		line += "  ·  " + info.Mode + " mode"
	}
	if info.Adapter != "" {
		line += "  ·  " + info.Adapter + " adapter"
	}
	b.WriteString(line)
	b.WriteString("\n\n")

	// Best-effort: the banner is cosmetic, so a failed write to stderr/stdout
	// is not actionable.
	_, _ = io.WriteString(w, b.String())
}

func displayWidth(s string) int {
	return utf8.RuneCountInString(ansiPattern.ReplaceAllString(s, ""))
}

// centerArt left-pads each row so the block is horizontally centered in a
// terminal of `cols` columns. With cols <= 0 (no TTY) or a terminal no
// wider than the art, the block is returned unchanged so piped output and
// narrow terminals fall back to the left-aligned rendering.
func centerArt(block string, cols int) string {
	if cols <= 0 {
		return block
	}
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	maxw := 0
	for _, ln := range lines {
		if w := displayWidth(ln); w > maxw {
			maxw = w
		}
	}
	if cols <= maxw {
		return block
	}
	pad := strings.Repeat(" ", (cols-maxw)/2)
	var b strings.Builder
	for i, ln := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		if ln != "" {
			b.WriteString(pad)
		}
		b.WriteString(ln)
	}
	b.WriteByte('\n')
	return b.String()
}

func colorEnabled(w io.Writer) bool {
	if v, ok := os.LookupEnv("NO_COLOR"); ok && v != "" {
		return false
	}
	if v, ok := os.LookupEnv("FORCE_COLOR"); ok && v != "" {
		return true
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
