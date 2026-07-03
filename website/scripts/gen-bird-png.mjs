// Renders public/portwing-bird-banner.png from the real CLI banner art at
// internal/banner/portwing.ans — the SAME pixels `portwing` prints on a
// truecolor terminal at startup. When the website is converted to the
// CodesWhat web shell, the CLI demo shows this PNG (crisp, pixelated)
// instead of tiling half-block text in a <pre>, which the browser renders
// with sub-pixel scanlines and the wrong cell aspect. Run from website/:
//
//   node scripts/gen-bird-png.mjs        (requires ImageMagick `magick` on PATH)
//
// portwing.ans is 24-bit truecolor half-block art: each text cell carries an
// upper-half ▀ (fg) / lower-half ▄ (fg) glyph over an optional bg, so one text
// row = two vertical pixels. We rebuild the raw RGBA bitmap (transparent where
// empty) and pipe it to magick to encode the PNG. (Mirrors sockguard's
// gen-dog-png.mjs; portwing has no app/ subdir so the source is one level up.)

import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const HERE = dirname(fileURLToPath(import.meta.url));
const SRC = resolve(HERE, "../../internal/banner/portwing.ans");
const OUT = resolve(HERE, "../public/portwing-bird-banner.png");

const raw = readFileSync(SRC, "utf8").replace(/\n$/, "");
const lines = raw.split("\n");

// Parse one line into cells, tracking the running 24-bit fg/bg SGR state.
function parseLine(line) {
  let fg = null;
  let bg = null;
  const cells = [];
  let i = 0;
  while (i < line.length) {
    if (line[i] === "\x1b") {
      const m = /^\x1b\[([0-9;]*)m/.exec(line.slice(i));
      if (m) {
        const ps = m[1].split(";").map((x) => (x === "" ? 0 : Number(x)));
        for (let k = 0; k < ps.length; k++) {
          const p = ps[k];
          if (p === 0) {
            fg = null;
            bg = null;
          } else if (p === 39) {
            fg = null;
          } else if (p === 49) {
            bg = null;
          } else if (p === 38 && ps[k + 1] === 2) {
            fg = [ps[k + 2], ps[k + 3], ps[k + 4]];
            k += 4;
          } else if (p === 48 && ps[k + 1] === 2) {
            bg = [ps[k + 2], ps[k + 3], ps[k + 4]];
            k += 4;
          }
        }
        i += m[0].length;
        continue;
      }
    }
    cells.push({ ch: line[i], fg, bg });
    i += 1;
  }
  return cells;
}

const parsed = lines.map(parseLine);
const W = Math.max(...parsed.map((c) => c.length));
const H = parsed.length * 2;
const buf = Buffer.alloc(W * H * 4); // RGBA, zero-filled = fully transparent

function setPx(x, y, c) {
  if (!c) return; // transparent
  const o = (y * W + x) * 4;
  buf[o] = c[0];
  buf[o + 1] = c[1];
  buf[o + 2] = c[2];
  buf[o + 3] = 255;
}

parsed.forEach((cells, r) => {
  cells.forEach((cell, x) => {
    let top = null;
    let bot = null;
    if (cell.ch === "▀") {
      top = cell.fg;
      bot = cell.bg;
    } else if (cell.ch === "▄") {
      top = cell.bg;
      bot = cell.fg;
    } else if (cell.ch === "█") {
      top = cell.fg;
      bot = cell.fg;
    } else {
      top = cell.bg;
      bot = cell.bg;
    }
    setPx(x, 2 * r, top);
    setPx(x, 2 * r + 1, bot);
  });
});

execFileSync("magick", ["-size", `${W}x${H}`, "-depth", "8", "rgba:-", OUT], { input: buf });
console.log(`wrote ${OUT} (${W}x${H})`);
