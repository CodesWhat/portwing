// After `next build` in docs/, the static export is what ships at /docs. Moving
// the content under content/docs/current/ must not leak that directory into
// the output, every current page must still land at its unversioned URL, and
// each archived content/docs/vX.Y/ must export under its own segment: pages at
// vX.Y/<slug>.html and the version's index at vX.Y.html (the segment is the
// slug of its index page, so Next names it like a page, not a directory index).
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const CURRENT_DIR = "current";
const VERSION_DIR = /^v\d+\.\d+$/u;
const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const DOCS_CONTENT = path.join(ROOT, "docs", "content", "docs");
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

// Every page file of one version dir, keyed by the HTML file it must export as.
// `prefix` is the URL segment the version serves under: none for current/.
function expectedPages(dir, prefix) {
  const expected = new Map();
  const pages = listFiles(path.join(DOCS_CONTENT, dir)).filter((file) => /\.mdx?$/u.test(file));
  for (const page of pages) {
    const slugs = [...prefix, ...slugsFor(page)];
    expected.set(slugs.length === 0 ? "index.html" : `${slugs.join("/")}.html`, `${dir}/${page}`);
  }
  return expected;
}

const problems = [];

if (!fs.existsSync(OUT)) {
  problems.push(`${path.relative(ROOT, OUT)} is missing; run the docs build first`);
} else {
  const expected = expectedPages(CURRENT_DIR, []);
  if (!expected.has("index.html")) {
    problems.push(`${CURRENT_DIR}/index.mdx is missing; /docs would have no landing page`);
  }
  const archives = fs
    .readdirSync(DOCS_CONTENT, { withFileTypes: true })
    .filter((entry) => entry.isDirectory() && VERSION_DIR.test(entry.name))
    .map((entry) => entry.name);
  for (const version of archives) {
    for (const [html, page] of expectedPages(version, [version])) expected.set(html, page);
    if (!fs.existsSync(path.join(DOCS_CONTENT, version, "index.mdx"))) {
      problems.push(`${version}/index.mdx is missing; /docs/${version} would have no landing page`);
    }
  }
  const exported = new Set(
    listFiles(OUT)
      .map((file) => file.split(path.sep).join("/"))
      .filter((file) => file.endsWith(".html") && !file.startsWith("_next/"))
      .filter((file) => !FRAMEWORK_PAGES.has(file)),
  );
  for (const [html, page] of expected) {
    if (!exported.has(html)) {
      problems.push(`${page} must export as docs/out/${html}`);
    }
  }
  for (const html of exported) {
    if (!expected.has(html)) {
      problems.push(
        `docs/out/${html} has no page in ${CURRENT_DIR}/ or an archive; a directory leaked into the URL, a page went missing from the source, or an archive was removed without a clean build`,
      );
    }
  }
}

if (problems.length > 0) {
  for (const problem of problems) console.error(`FAIL: ${problem}`);
  process.exit(1);
}
console.log("Docs export contract checks passed.");
