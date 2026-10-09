// After `next build` in docs/, the static export is what ships at /docs. Moving
// the content under content/docs/current/ must not leak that directory into
// the output, and every current page must still land at its unversioned URL.
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const CURRENT_DIR = "current";
const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const CONTENT = path.join(ROOT, "docs", "content", "docs", CURRENT_DIR);
const OUT = path.join(ROOT, "docs", "out");

// Mirrors slugsForPath in docs/src/lib/docs-versions.ts, which the loader uses.
// Kept separate on purpose: the contract should catch the loader drifting.
function slugsFor(file) {
  const segments = file
    .replace(/\.mdx?$/u, "")
    .split(path.sep)
    .filter((segment) => !/^\(.+\)$/u.test(segment));
  if (segments.at(-1) === "index") segments.pop();
  return segments;
}

function listFiles(dir) {
  return fs
    .readdirSync(dir, { recursive: true, withFileTypes: true })
    .filter((entry) => entry.isFile())
    .map((entry) => path.relative(dir, path.join(entry.parentPath, entry.name)));
}

// Pages Next emits for itself, not from content.
const FRAMEWORK_PAGES = new Set(["404.html", "_not-found.html"]);
// An archived release line exports under its own segment and is checked by its
// own contract; this one only owns the unversioned pages.
const ARCHIVE = /^v\d+\.\d+(?:\/|\.html$)/u;

const problems = [];

if (!fs.existsSync(OUT)) {
  problems.push(`${path.relative(ROOT, OUT)} is missing; run the docs build first`);
} else {
  const pages = listFiles(CONTENT).filter((file) => /\.mdx?$/u.test(file));
  const expected = new Map();
  for (const page of pages) {
    const slugs = slugsFor(page);
    expected.set(slugs.length === 0 ? "index.html" : `${slugs.join("/")}.html`, page);
  }
  if (!expected.has("index.html")) {
    problems.push(`${CURRENT_DIR}/index.mdx is missing; /docs would have no landing page`);
  }
  const exported = new Set(
    listFiles(OUT)
      .map((file) => file.split(path.sep).join("/"))
      .filter((file) => file.endsWith(".html") && !file.startsWith("_next/"))
      .filter((file) => !FRAMEWORK_PAGES.has(file) && !ARCHIVE.test(file)),
  );
  for (const [html, page] of expected) {
    if (!exported.has(html)) {
      problems.push(`${CURRENT_DIR}/${page} must export as docs/out/${html}`);
    }
  }
  for (const html of exported) {
    if (!expected.has(html)) {
      problems.push(
        `docs/out/${html} has no page in ${CURRENT_DIR}/; a directory leaked into the URL or a page went missing from the source`,
      );
    }
  }
}

if (problems.length > 0) {
  for (const problem of problems) console.error(`FAIL: ${problem}`);
  process.exit(1);
}
console.log("Docs export contract checks passed.");
