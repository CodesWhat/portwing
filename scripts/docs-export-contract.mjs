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
  const segments = file.replace(/\.mdx?$/u, "").split(path.sep);
  if (segments.at(-1) === "index") segments.pop();
  return segments;
}

function listFiles(dir) {
  return fs
    .readdirSync(dir, { recursive: true, withFileTypes: true })
    .filter((entry) => entry.isFile())
    .map((entry) => path.relative(dir, path.join(entry.parentPath, entry.name)));
}

const problems = [];

if (!fs.existsSync(OUT)) {
  problems.push(`${path.relative(ROOT, OUT)} is missing; run the docs build first`);
} else {
  const pages = listFiles(CONTENT).filter((file) => /\.mdx?$/u.test(file));
  for (const page of pages) {
    const slugs = slugsFor(page);
    const html = slugs.length === 0 ? "index.html" : `${slugs.join("/")}.html`;
    if (!fs.existsSync(path.join(OUT, html))) {
      problems.push(`current/${page} must export as docs/out/${html}`);
    }
  }
  for (const entry of fs.readdirSync(OUT, { recursive: true })) {
    if (path.basename(String(entry)).startsWith(CURRENT_DIR)) {
      problems.push(`docs/out/${entry} leaks the ${CURRENT_DIR} directory into the export`);
    }
  }
}

if (problems.length > 0) {
  for (const problem of problems) console.error(`FAIL: ${problem}`);
  process.exit(1);
}
console.log("Docs export contract checks passed.");
