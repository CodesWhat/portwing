import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import remarkMdx from "remark-mdx";
import remarkParse from "remark-parse";
import remarkStringify from "remark-stringify";
import { unified } from "unified";
import { archiveVersionOf } from "../docs/src/lib/docs-versions.ts";
import { remarkVersionedLinks, scopeLink } from "../docs/src/lib/remark-versioned-links.ts";

const ROOT = path.resolve(import.meta.dirname, "..");
const CURRENT = path.join(ROOT, "docs", "content", "docs", "current");

// Parse, run the plugin as Fumadocs would (with the source file's path on the
// vfile), and write the tree back to MDX so assertions read like the source.
function run(source, filePath) {
  const processor = unified()
    .use(remarkParse)
    .use(remarkMdx)
    .use(remarkVersionedLinks)
    .use(remarkStringify);
  return String(processor.processSync({ value: source, path: filePath }));
}

test("archiveVersionOf recognises vX.Y directories and nothing else", () => {
  assert.equal(archiveVersionOf("v0.9/authentication.mdx"), "v0.9");
  assert.equal(archiveVersionOf("v1.10/guides/setup.mdx"), "v1.10");
  assert.equal(archiveVersionOf("/repo/docs/content/docs/v0.9/index.mdx"), "v0.9");
  assert.equal(
    archiveVersionOf("C:\\repo\\docs\\content\\docs\\v0.9\\index.mdx".replaceAll("\\", "/")),
    "v0.9",
  );
  assert.equal(archiveVersionOf("current/authentication.mdx"), undefined);
  assert.equal(archiveVersionOf("/repo/docs/content/docs/current/v0.9/x.mdx"), undefined);
  assert.equal(archiveVersionOf("v0.9.1/x.mdx"), undefined);
  assert.equal(archiveVersionOf("v0.9"), undefined);
  assert.equal(archiveVersionOf("index.mdx"), undefined);
});

test("scopeLink rewrites unversioned docs paths only", () => {
  assert.equal(scopeLink("/authentication", "v0.9"), "/v0.9/authentication");
  assert.equal(scopeLink("/authentication#keys", "v0.9"), "/v0.9/authentication#keys");
  assert.equal(scopeLink("/authentication?x=1#keys", "v0.9"), "/v0.9/authentication?x=1#keys");
  assert.equal(scopeLink("/", "v0.9"), "/v0.9");
  assert.equal(scopeLink("/#top", "v0.9"), "/v0.9#top");
  assert.equal(scopeLink("/v0.9/authentication", "v0.9"), "/v0.9/authentication");
  assert.equal(scopeLink("/v0.8/authentication", "v0.9"), "/v0.8/authentication");
  assert.equal(scopeLink("/v0.9", "v0.9"), "/v0.9");
  assert.equal(scopeLink("/portwing.png", "v0.9"), "/portwing.png");
  assert.equal(scopeLink("/files/portwing.service", "v0.9"), "/files/portwing.service");
  assert.equal(scopeLink("https://example.com/x", "v0.9"), "https://example.com/x");
  assert.equal(scopeLink("//example.com/x", "v0.9"), "//example.com/x");
  assert.equal(scopeLink("mailto:a@b.c", "v0.9"), "mailto:a@b.c");
  assert.equal(scopeLink("#anchor", "v0.9"), "#anchor");
  assert.equal(scopeLink("./sibling", "v0.9"), "./sibling");
  assert.equal(scopeLink("sibling", "v0.9"), "sibling");
});

const SAMPLE = [
  "[Auth](/authentication) and [anchor](/authentication#keys).",
  "",
  "[External](https://example.com/authentication) and [asset](/portwing.png).",
  "",
  "![logo](/portwing.png)",
  "",
  "[ref][r]",
  "",
  "[r]: /configuration",
  "",
  '<Card href="/security-model" title="x" />',
  "",
  "```md",
  "[not a link](/authentication)",
  "```",
  "",
  "`[inline](/authentication)`",
  "",
].join("\n");

test("links in an archived page are scoped to its version", () => {
  const out = run(SAMPLE, "/repo/docs/content/docs/v0.9/authentication.mdx");
  assert.match(out, /\[Auth\]\(\/v0\.9\/authentication\)/u);
  assert.match(out, /\[anchor\]\(\/v0\.9\/authentication#keys\)/u);
  assert.match(out, /\[External\]\(https:\/\/example\.com\/authentication\)/u);
  assert.match(out, /\[asset\]\(\/portwing\.png\)/u);
  assert.match(out, /!\[logo\]\(\/portwing\.png\)/u);
  assert.match(out, /\[r\]: \/v0\.9\/configuration/u);
  assert.match(out, /href="\/v0\.9\/security-model"/u);
  // Code is not a link: both forms are left exactly as written.
  assert.match(out, /\[not a link\]\(\/authentication\)/u);
  assert.match(out, /`\[inline\]\(\/authentication\)`/u);
});

test("the plugin is a no-op for current/ and for paths with no version", () => {
  for (const file of [
    "/repo/docs/content/docs/current/authentication.mdx",
    "current/authentication.mdx",
    undefined,
  ]) {
    const scoped = run(SAMPLE, file);
    const untouched = String(
      unified().use(remarkParse).use(remarkMdx).use(remarkStringify).processSync(SAMPLE),
    );
    assert.equal(scoped, untouched, `${file} must come through unchanged`);
  }
});

test("every real current/ page comes through the plugin byte-for-byte", () => {
  const pages = fs.readdirSync(CURRENT).filter((name) => name.endsWith(".mdx"));
  assert.ok(pages.length > 0);
  for (const name of pages) {
    const source = fs.readFileSync(path.join(CURRENT, name), "utf8");
    const file = path.join(CURRENT, name);
    const plain = String(
      unified()
        .use(remarkParse)
        .use(remarkMdx)
        .use(remarkStringify)
        .processSync({ value: source, path: file }),
    );
    assert.equal(run(source, file), plain, `${name} changed under the plugin`);
  }
});

test("a real current page scoped as an archive keeps every link inside the archive", () => {
  const source = fs.readFileSync(path.join(CURRENT, "authentication.mdx"), "utf8");
  const out = run(source, "/repo/docs/content/docs/v9.9/authentication.mdx");
  const internal = [...out.matchAll(/\]\((\/[^)\s]*)\)/gu)].map((match) => match[1]);
  assert.ok(internal.length > 0, "authentication.mdx should link to other docs pages");
  for (const target of internal) {
    assert.ok(target.startsWith("/v9.9/"), `${target} escaped the archive`);
  }
});

test("the plugin is registered globally so the Fumadocs preset stays on", () => {
  // A collection-level mdxOptions replaces the preset instead of extending it:
  // the build stays green but code blocks lose highlighting and pages lose
  // their table of contents. Only defineConfig({ mdxOptions }) layers on top.
  const config = fs.readFileSync(path.join(ROOT, "docs", "source.config.ts"), "utf8");
  const docsBlock = config.slice(
    config.indexOf("defineDocs("),
    config.indexOf("});", config.indexOf("defineDocs(")),
  );
  assert.doesNotMatch(docsBlock, /mdxOptions/u);
  assert.match(config, /defineConfig\(\{\s*mdxOptions:\s*\{[^}]*remarkVersionedLinks/u);
});
