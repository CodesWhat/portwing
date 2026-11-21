// SPDX-License-Identifier: MIT

import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test, { after } from "node:test";

import {
  CANONICAL_ORIGIN,
  CTA_TARGETS,
  checkCtas,
  checkExternal,
  checkLinks,
  checkSite,
  checkSitemap,
  extractLinks,
  PAGE_CTAS,
  parseSitemapLocs,
  routeFile,
} from "./site-link-contract.mjs";

const made = [];
after(() => {
  for (const root of made) fs.rmSync(root, { recursive: true, force: true });
});

function site(files) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "site-link-contract-test-"));
  made.push(root);
  for (const [name, body] of Object.entries(files)) {
    const file = path.join(root, name);
    fs.mkdirSync(path.dirname(file), { recursive: true });
    fs.writeFileSync(file, body);
  }
  return root;
}

const sitemap = (...paths) =>
  `<urlset>${paths.map((p) => `<url><loc>${CANONICAL_ORIGIN}${p}</loc></url>`).join("")}</urlset>`;
const robots = `User-Agent: *\nAllow: /\n\nSitemap: ${CANONICAL_ORIGIN}/sitemap.xml\n`;
const page = (body) => `<html><head></head><body>${body}</body></html>`;

test("sitemap locs are read and entity-decoded", () => {
  assert.deepEqual(parseSitemapLocs("<loc>https://a/x?a=1&amp;b=2</loc><loc> https://a/y </loc>"), [
    "https://a/x?a=1&b=2",
    "https://a/y",
  ]);
});

test("routes resolve as the static host serves them", () => {
  const root = site({
    "index.html": "",
    "compare.html": "",
    "compare/komodo.html": "",
    "docs/index.html": "",
    "docs/getting-started.html": "",
    "llms.txt": "",
  });
  assert.ok(routeFile(root, "/")?.endsWith("index.html"));
  assert.ok(routeFile(root, "/compare")?.endsWith("compare.html"));
  assert.ok(routeFile(root, "/compare/")?.endsWith("compare.html"));
  assert.ok(routeFile(root, "/compare/komodo")?.endsWith("komodo.html"));
  assert.ok(routeFile(root, "/docs")?.endsWith(path.join("docs", "index.html")));
  assert.ok(routeFile(root, "/docs/")?.endsWith(path.join("docs", "index.html")));
  assert.ok(routeFile(root, "/llms.txt"));
  assert.equal(routeFile(root, "/missing"), null);
  assert.equal(routeFile(root, "/docs/missing"), null);
  assert.equal(routeFile(root, "/../etc/passwd"), null);
  assert.equal(routeFile(root, "/%E0%A4%A"), null);
});

test("a sitemap entry that 404s fails, and a good one passes", () => {
  const good = site({
    "index.html": "",
    "docs/index.html": "",
    "sitemap.xml": sitemap("", "/docs"),
    "robots.txt": robots,
  });
  assert.deepEqual(checkSitemap(good), []);

  const bad = site({
    "index.html": "",
    "sitemap.xml": sitemap("", "/docs/gone"),
    "robots.txt": robots,
  });
  assert.deepEqual(checkSitemap(bad), [`sitemap entry 404s: ${CANONICAL_ORIGIN}/docs/gone`]);
});

test("sitemap entries must sit on the canonical origin and be unique", () => {
  const root = site({
    "index.html": "",
    "sitemap.xml":
      "<urlset><loc>https://example.com/</loc><loc>https://portwing.codeswhat.com</loc>" +
      "<loc>https://portwing.codeswhat.com</loc></urlset>",
    "robots.txt": robots,
  });
  const problems = checkSitemap(root);
  assert.equal(problems.length, 2);
  assert.match(problems[0], /off the canonical origin/);
  assert.match(problems[1], /twice/);
});

test("robots.txt must point at the canonical sitemap", () => {
  const files = { "index.html": "", "sitemap.xml": sitemap("") };
  assert.match(checkSitemap(site(files)).join(), /robots\.txt was not exported/);
  const wrongHost = site({
    ...files,
    "robots.txt": "Sitemap: https://example.com/sitemap.xml\n",
  });
  assert.match(checkSitemap(wrongHost).join(), /robots\.txt must point at/);
  const wrongPath = site({
    ...files,
    "robots.txt": `Sitemap: ${CANONICAL_ORIGIN}/sitemap-index.xml\n`,
  });
  assert.match(checkSitemap(wrongPath).join(), /robots\.txt must point at/);
  assert.deepEqual(checkSitemap(site({ ...files, "robots.txt": robots })), []);
  assert.deepEqual(checkSitemap(site({ "robots.txt": robots })), ["sitemap.xml was not exported"]);
});

test("links are extracted from anchors and canonical links only", () => {
  const links = extractLinks(
    `<link rel="canonical" href="/x"/><link rel="stylesheet" href="/a.css"/>` +
      `<a class="b" href="/docs?a=1&amp;b=2">x</a><a name="y">no href</a>`,
  );
  assert.deepEqual(
    links.map((link) => [link.kind, link.href]),
    [
      ["a", "/docs?a=1&b=2"],
      ["canonical", "/x"],
    ],
  );
});

test("internal links must resolve, including relative ones and fragments", () => {
  const root = site({
    "index.html": page(
      `<h2 id="faq">FAQ</h2>` +
        `<a href="/docs">ok</a><a href="/docs/">ok</a><a href="#faq">ok</a><a href="/docs/guide#step">ok</a>` +
        `<a href="${CANONICAL_ORIGIN}/docs/guide">ok</a>`,
    ),
    "docs/index.html": page(`<a href="/">home</a>`),
    "docs/guide.html": page(`<h2 id="step">Step</h2><a href="other">relative</a>`),
    "docs/other.html": page(""),
  });
  assert.deepEqual(checkLinks(root), []);

  const broken = site({
    "index.html": page(
      `<a href="/nope">a</a><a href="#missing">b</a><a href="/docs/guide#gone">c</a>` +
        `<a href="#">d</a><a href="">e</a>`,
    ),
    "docs/guide.html": page(""),
  });
  const problems = checkLinks(broken);
  assert.equal(problems.length, 5);
  assert.match(problems[0], /\/nope: no exported file/);
  assert.match(problems[1], /#missing: no element with id/);
  assert.match(problems[2], /#gone: no element with id "gone"/);
  assert.match(problems[3], /empty href/);
  assert.match(problems[4], /empty href/);
});

test("the 404 pages Next emits are not link-checked", () => {
  const root = site({
    "index.html": page(""),
    "404.html": page(`<a href="/nowhere">x</a>`),
    "_not-found.html": page(`<a href="/nowhere">x</a>`),
  });
  assert.deepEqual(checkLinks(root), []);
});

test("canonical links must resolve and name their own page, except on archived docs", () => {
  const canonical = (target) => `<link rel="canonical" href="${CANONICAL_ORIGIN}${target}"/>`;
  const root = site({
    "index.html": canonical(""),
    "compare.html": canonical("/docs"),
    "docs/index.html": canonical("/docs"),
    "docs/v1.0.html": canonical("/docs"),
  });
  assert.deepEqual(checkLinks(root), [
    `/compare: canonical ${CANONICAL_ORIGIN}/docs does not name this page`,
  ]);
});

test("external links must be https URLs to expected hosts", () => {
  for (const ok of [
    "https://github.com/CodesWhat/portwing",
    "https://github.com/codeswhat/sockguard",
    "https://github.com/sponsors/CodesWhat",
    "https://github.com/orgs/CodesWhat/packages/container/package/portwing",
    "https://github.com/portainer/portainer/releases/tag/2.45.3",
    "https://discord.gg/mWHCPJRzSx",
    "https://codeswhat.com",
    "mailto:security@codeswhat.com",
  ]) {
    assert.equal(checkExternal(ok), null, ok);
  }
  assert.match(checkExternal("http://github.com/CodesWhat/portwing"), /not https/);
  assert.match(checkExternal("javascript:alert(1)"), /not https/);
  assert.match(checkExternal("https://github.com/"), /without an owner/);
  assert.match(checkExternal("https://github.com/someone-else/repo"), /unexpected GitHub owner/);
  assert.match(checkExternal("https://github.com/sponsors/someone"), /unexpected GitHub sponsors/);
  assert.match(checkExternal("https://discord.gg.evil.dev/x"), /unexpected host/);
  assert.match(checkExternal("https://example.com/"), /placeholder host/);
  assert.match(checkExternal("https://localhost:3000/"), /placeholder host/);
  assert.match(checkExternal("https://unknown.dev/"), /unexpected host/);
  assert.match(checkExternal("https://user:pw@github.com/CodesWhat"), /credentials/);
  assert.match(checkExternal("mailto:someone@gmail.com"), /outside codeswhat\.com/);
  assert.match(checkExternal("https://"), /malformed/);
});

test("external anchors in the page are checked, and target=_blank needs noopener", () => {
  const root = site({
    "index.html": page(
      `<a href="https://github.com/CodesWhat/portwing" target="_blank" rel="noopener noreferrer">ok</a>` +
        `<a href="https://github.com/CodesWhat/portwing" target="_blank">bare</a>` +
        `<a href="http://github.com/CodesWhat/portwing">plain</a>` +
        `<a href="//github.com/CodesWhat/portwing">relative scheme</a>`,
    ),
  });
  const problems = checkLinks(root);
  assert.equal(problems.length, 3);
  assert.match(problems[0], /target=_blank without rel=noopener/);
  assert.match(problems[1], /not https/);
  assert.match(problems[2], /protocol-relative/);
});

test("every CTA in the analytics contract has a target and the pages carry them", () => {
  const contract = fs.readFileSync(
    path.resolve(import.meta.dirname, "..", "analytics", "src", "contract.ts"),
    "utf8",
  );
  const combinations = [...contract.matchAll(/^\s+"([a-z_]+):[a-z_]+",$/gmu)].map((m) => m[1]);
  assert.ok(combinations.length > 0);
  for (const id of new Set(combinations)) {
    assert.ok(id in CTA_TARGETS, `${id} has no entry in CTA_TARGETS`);
  }
  for (const ids of Object.values(PAGE_CTAS)) {
    for (const id of ids) assert.ok(CTA_TARGETS[id], `${id} is a copy button, not a link`);
  }

  const hrefs = (ids) => ids.map((id) => `<a href="${CTA_TARGETS[id]}">${id}</a>`).join("");
  const complete = site({
    "index.html": page(hrefs(PAGE_CTAS["index.html"])),
    "docs/index.html": page(hrefs(PAGE_CTAS["docs/index.html"])),
  });
  assert.deepEqual(checkCtas(complete), []);

  const missing = site({
    "index.html": page(hrefs(PAGE_CTAS["index.html"].filter((id) => id !== "community_discord"))),
    "docs/index.html": page(hrefs(PAGE_CTAS["docs/index.html"])),
  });
  assert.deepEqual(checkCtas(missing), [
    "index.html: no link to https://discord.gg/mWHCPJRzSx for CTA community_discord",
  ]);
  assert.match(checkCtas(site({})).join("\n"), /was not exported/);
});

test("checkSite reports a missing build output instead of passing", () => {
  assert.match(checkSite(path.join(os.tmpdir(), "no-such-site-output"))[0], /run npm run build/);
});

test("llms.txt own-origin URLs must resolve", () => {
  const root = site({
    "index.html": page(""),
    "docs/index.html": page(""),
    "sitemap.xml": sitemap("", "/docs"),
    "robots.txt": robots,
    "llms.txt": `See ${CANONICAL_ORIGIN}/docs, ${CANONICAL_ORIGIN}/docs/gone.`,
  });
  assert.deepEqual(
    checkSite(root).filter((p) => p.startsWith("llms.txt")),
    [`llms.txt: ${CANONICAL_ORIGIN}/docs/gone 404s`],
  );
});
