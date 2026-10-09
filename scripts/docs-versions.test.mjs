import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

const ROOT = path.resolve(import.meta.dirname, "..");
const DOCS_ROOT = path.join(ROOT, "docs", "content", "docs");
const VERSION_DIR = /^v\d+\.\d+$/u;
const PAGE_FILE = /\.mdx?$/u;

function read(relativePath) {
  return fs.readFileSync(path.join(ROOT, relativePath), "utf8");
}

// Version directories under content/docs: `current` plus archived vX.Y trees.
function versionDirs() {
  return fs
    .readdirSync(DOCS_ROOT, { withFileTypes: true })
    .filter((entry) => entry.isDirectory())
    .map((entry) => entry.name);
}

// Page slugs of one version dir, relative to that dir ("index" is the root page).
function pageSlugs(dir) {
  return fs
    .readdirSync(path.join(DOCS_ROOT, dir), { recursive: true, withFileTypes: true })
    .filter((entry) => entry.isFile() && PAGE_FILE.test(entry.name))
    .map((entry) =>
      path
        .relative(path.join(DOCS_ROOT, dir), path.join(entry.parentPath, entry.name))
        .replace(PAGE_FILE, "")
        .split(path.sep)
        .join("/"),
    )
    .sort();
}

// The public URL path of every current page, without the /docs prefix.
function currentPublicSlugs() {
  return pageSlugs("current")
    .filter((slug) => slug !== "index")
    .sort();
}

function stringsIn(source, startPattern) {
  const start = source.search(startPattern);
  assert.notEqual(start, -1, `${startPattern} not found`);
  const end = source.indexOf("]", start);
  return [...source.slice(start, end).matchAll(/"([^"]*)"/gu)].map((m) => m[1]);
}

test("every docs page lives in current/ or a vX.Y/ directory", () => {
  const entries = fs.readdirSync(DOCS_ROOT, { withFileTypes: true });
  for (const entry of entries) {
    if (entry.isDirectory()) {
      assert.ok(
        entry.name === "current" || VERSION_DIR.test(entry.name),
        `${entry.name}/ must be current or vX.Y`,
      );
    } else {
      assert.equal(entry.name, "meta.json", `${entry.name} sits outside current/ and vX.Y/`);
    }
  }
  assert.ok(versionDirs().includes("current"));
  const rootMeta = JSON.parse(fs.readFileSync(path.join(DOCS_ROOT, "meta.json"), "utf8"));
  assert.ok(rootMeta.pages.includes("current"));
  for (const dir of versionDirs()) {
    const meta = JSON.parse(fs.readFileSync(path.join(DOCS_ROOT, dir, "meta.json"), "utf8"));
    assert.equal(meta.root, true, `${dir}/meta.json must be a root folder`);
    assert.equal(typeof meta.title, "string", `${dir}/meta.json needs a title`);
    assert.ok(rootMeta.pages.includes(dir), `content/docs/meta.json must list ${dir}`);
  }
});

test("the sitemap lists exactly the current docs pages", () => {
  const sitemap = read("website/src/app/sitemap.ts");
  const listed = stringsIn(sitemap, /const DOCS_SLUGS = \[/u).sort();
  assert.deepEqual(listed, currentPublicSlugs());
});

test("the analytics docs allowlist is exactly the current docs pages", () => {
  const contract = read("analytics/src/contract.ts");
  const listed = stringsIn(contract, /const DOCS_PATHS = new Set\(\[/u).sort();
  const expected = ["/docs", ...currentPublicSlugs().map((slug) => `/docs/${slug}`)].sort();
  assert.deepEqual(listed, expected);
});

// Source of one page with fenced and inline code removed, so examples that
// show a link do not count as one.
function proseOf(file) {
  return fs
    .readFileSync(file, "utf8")
    .replace(/```[\s\S]*?```/gu, "")
    .replace(/`[^`\n]*`/gu, "");
}

function allPages() {
  const pages = [];
  for (const dir of versionDirs()) {
    for (const slug of pageSlugs(dir)) {
      for (const extension of ["mdx", "md"]) {
        const file = path.join(DOCS_ROOT, dir, `${slug}.${extension}`);
        if (fs.existsSync(file)) pages.push({ label: `${dir}/${slug}`, file });
      }
    }
  }
  return pages;
}

test("root-relative links resolve to a page in the version they name or sit in", () => {
  // Archived pages keep the unscoped links they had at their release tag;
  // docs/src/lib/remark-versioned-links.ts rewrites them to /vX.Y/... at build.
  // So an unversioned link is checked against its own dir's page set. A link
  // that names another version on purpose (/v0.8/x inside v0.9/) is checked
  // against that version's page set, and fails if that archive does not exist.
  const dirs = new Set(versionDirs());
  const sets = new Map();
  const setFor = (dir) => {
    if (!sets.has(dir)) sets.set(dir, new Set(pageSlugs(dir)));
    return sets.get(dir);
  };
  const dead = [];
  for (const dir of dirs) {
    for (const slug of setFor(dir)) {
      const file = path.join(DOCS_ROOT, dir, `${slug}.mdx`);
      const source = (fs.existsSync(file) ? fs.readFileSync(file, "utf8") : "").replace(
        /```[\s\S]*?```/gu,
        "",
      );
      for (const match of source.matchAll(/\]\((\/[^)\s]*)\)/gu)) {
        const [target] = match[1].split(/[?#]/u);
        let resolvedDir = dir;
        let relative = target.slice(1);
        const named = /^(v\d+\.\d+)(?:\/|$)/u.exec(relative);
        if (named) {
          resolvedDir = named[1];
          relative = relative.slice(named[0].length);
          if (!dirs.has(resolvedDir)) {
            dead.push(
              `${dir}/${slug}: ${match[1]} (no ${resolvedDir}/ archive exists to link into)`,
            );
            continue;
          }
        }
        const resolved = relative.replace(/\/$/u, "") || "index";
        if (!setFor(resolvedDir).has(resolved)) {
          dead.push(`${dir}/${slug}: ${match[1]}`);
        }
      }
    }
  }
  assert.deepEqual(dead, []);
});

test("no page uses a raw <a> with a root-relative href", () => {
  // A native <a> gets no basePath from Next, so /authentication points at the
  // site root, not /docs/authentication, and the archive link scoping skips
  // native anchors. Use a markdown link: [text](/page).
  const raw = /<a\b[^>]*\bhref\s*=\s*(?:\{\s*)?["'`]\//u;
  const found = allPages()
    .filter(({ file }) => raw.test(proseOf(file)))
    .map(({ label }) => label);
  assert.deepEqual(found, [], "use a markdown link instead of a raw <a href='/...'>");
});

test("no page links to /docs/... directly", () => {
  // Next adds the /docs basePath to a root-relative link, so /docs/x becomes
  // /docs/docs/x. Archive scoping also leaves /docs/... untouched. Write the
  // target without the prefix: [text](/page).
  const prefixed = /(?:\]\(|\bhref\s*=\s*(?:\{\s*)?["'`])\/docs(?=[/#?)"'`\s]|$)/u;
  const found = allPages()
    .filter(({ file }) => prefixed.test(proseOf(file)))
    .map(({ label }) => label);
  assert.deepEqual(found, [], "drop the /docs prefix from root-relative links");
});
