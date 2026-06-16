// Command gen renders the portwing logo PNG into a truecolor half-block
// ANSI art file (portwing.ans) that the banner package embeds.
//
// Each text row encodes two image sub-rows via the upper-half block ▀
// (foreground = top pixel, background = bottom pixel), doubling vertical
// resolution. Fully transparent cells become spaces so the silhouette
// floats on the terminal background. Fully blank rows at the top and
// bottom are trimmed.
//
// Regenerate after changing the logo:
//
//	go generate ./internal/banner/...
package main

import (
	"flag"
	"fmt"
	"image"
	_ "image/png"
	"os"
	"regexp"
	"strings"
)

func main() {
	src := flag.String("src", "../../portwing.png", "source logo PNG")
	out := flag.String("out", "portwing.ans", "output ANSI art file")
	width := flag.Int("width", 50, "render width in columns")
	flag.Parse()

	f, err := os.Open(*src)
	if err != nil {
		fatal(err)
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		fatal(err)
	}

	b := img.Bounds()
	cols := *width
	// Square cells keep the logo's aspect ratio: each text row is two
	// sub-rows tall, so the per-cell pixel height is 2× the width.
	cellW := float64(b.Dx()) / float64(cols)
	rows := int(float64(b.Dy()) / (2 * cellW))

	at := func(cx, sy int) (r, g, bl, a uint32) {
		px := b.Min.X + int((float64(cx)+0.5)*cellW)
		py := b.Min.Y + int((float64(sy)+0.5)*cellW)
		if px >= b.Max.X {
			px = b.Max.X - 1
		}
		if py >= b.Max.Y {
			py = b.Max.Y - 1
		}
		r, g, bl, a = img.At(px, py).RGBA()
		return r >> 8, g >> 8, bl >> 8, a >> 8
	}

	const thr = 128
	lines := make([]string, 0, rows)
	for ry := 0; ry < rows; ry++ {
		var sb strings.Builder
		for cx := 0; cx < cols; cx++ {
			tr, tg, tb, ta := at(cx, 2*ry)
			br, bg, bb, ba := at(cx, 2*ry+1)
			switch top, bot := ta >= thr, ba >= thr; {
			case top && bot:
				fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", tr, tg, tb, br, bg, bb)
			case top:
				fmt.Fprintf(&sb, "\x1b[49m\x1b[38;2;%d;%d;%dm▀", tr, tg, tb)
			case bot:
				fmt.Fprintf(&sb, "\x1b[49m\x1b[38;2;%d;%d;%dm▄", br, bg, bb)
			default:
				sb.WriteString("\x1b[0m ")
			}
		}
		sb.WriteString("\x1b[0m")
		lines = append(lines, sb.String())
	}

	lines = trimBlankRows(lines)

	var body strings.Builder
	for _, ln := range lines {
		body.WriteString(ln)
		body.WriteByte('\n')
	}
	if err := os.WriteFile(*out, []byte(body.String()), 0o644); err != nil { // #nosec G306 -- generated banner asset; world-readable (0644) is intended
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d rows × %d cols) from %s\n", *out, len(lines), cols, *src)
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func trimBlankRows(lines []string) []string {
	blank := func(s string) bool {
		return strings.TrimSpace(ansiRE.ReplaceAllString(s, "")) == ""
	}
	start, end := 0, len(lines)
	for start < end && blank(lines[start]) {
		start++
	}
	for end > start && blank(lines[end-1]) {
		end--
	}
	return lines[start:end]
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gen:", err)
	os.Exit(1)
}
