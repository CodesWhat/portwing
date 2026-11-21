// SPDX-License-Identifier: MIT

// Checks the exported site (website/out, which carries the docs export under
// /docs) the way a crawler or a visitor would: every sitemap entry and every
// link resolves to a file that was actually exported, robots.txt points at the
// real sitemap, and external links are well-formed https URLs to hosts we mean
// to link to. Nothing is fetched. The static host maps /x to x.html or
// x/index.html and serves both /x and /x/, so both spellings resolve.
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

export const CANONICAL_ORIGIN = "https://portwing.codeswhat.com";

const OWN_GITHUB_OWNERS = new Set(["codeswhat"]);

// Competitor and reference hosts the comparison pages and docs cite on purpose.
// A new host showing up in a link is a decision, so it fails until added here.
const EXTERNAL_HOSTS = new Map([
  ["github.com", null],
  ["codeswhat.com", null],
  ["discord.gg", null],
  ["opensource.org", null],
  ["docs.docker.com", null],
  ["docs.portainer.io", null],
  ["komo.do", null],
  ["getarcane.app", null],
  ["crazymax.dev", null],
  ["containrrr.dev", null],
  ["modelcontextprotocol.io", null],
  ["cyclonedx.org", null],
  ["securityscorecards.dev", null],
  ["www.bestpractices.dev", null],
  ["qlty.sh", null],
  ["codecov.io", null],
  ["www.star-history.com", null],
  ["getdrydock.com", null],
  ["getsockguard.com", null],
  ["pkg.go.dev", null],
]);

// github.com links are split by owner: ours, plus the projects the comparison
// pages cite.
const THIRD_PARTY_GITHUB_OWNERS = new Set([
  "nicholas-fedor",
  "portainer",
  "moghtech",
  "getarcaneapp",
  "finsys",
  "containrrr",
  "crazy-max",
  "dockmesh",
  "sigstore",
]);

const PLACEHOLDER_HOSTS = /(^|\.)(example\.(com|org|net)|localhost|invalid|test)$/iu;

// Pages Next emits for itself, which exist for the host's 404 handling and are
// not reachable routes.
const FRAMEWORK_PAGES = new Set(["404.html", "_not-found.html"]);

export function decodeEntities(value) {
  return value
    .replaceAll("&quot;", '"')
    .replaceAll("&#x27;", "'")
    .replaceAll("&#39;", "'")
    .replaceAll("&lt;", "<")
    .replaceAll("&gt;", ">")
    .replaceAll("&amp;", "&");
}

export function parseSitemapLocs(xml) {
  return [...xml.matchAll(/<loc>([^<]*)<\/loc>/gu)].map((match) => decodeEntities(match[1].trim()));
}

function isFile(target) {
  try {
    return fs.statSync(target).isFile();
  } catch {
    return false;
  }
}

// Returns the exported file a path is served from, or null for a 404.
export function routeFile(outputRoot, pathname) {
  let decoded;
  try {
    decoded = decodeURIComponent(pathname);
  } catch {
    return null;
  }
  if (!decoded.startsWith("/") || decoded.split("/").includes("..")) return null;
  const trimmed = decoded.length > 1 ? decoded.replace(/\/+$/u, "") : decoded;
  const root = path.resolve(outputRoot);
  const candidates =
    trimmed === "/"
      ? ["index.html"]
      : [`${trimmed.slice(1)}.html`, path.join(trimmed.slice(1), "index.html"), trimmed.slice(1)];
  for (const candidate of candidates) {
    const file = path.resolve(root, candidate);
    if (file.startsWith(`${root}${path.sep}`) && isFile(file)) return file;
  }
  return null;
}

function attributes(tag) {
  const found = {};
  for (const match of tag.matchAll(/([^\s"'<>/=]+)\s*=\s*(?:"([^"]*)"|'([^']*)')/gu)) {
    found[match[1].toLowerCase()] = decodeEntities(match[2] ?? match[3]);
  }
  return found;
}

export function extractLinks(html) {
  const links = [];
  for (const match of html.matchAll(/<a\b[^>]*>/giu)) {
    const attrs = attributes(match[0]);
    if (attrs.href !== undefined) links.push({ kind: "a", ...attrs });
  }
  for (const match of html.matchAll(/<link\b[^>]*>/giu)) {
    const attrs = attributes(match[0]);
    if (attrs.rel?.toLowerCase().split(/\s+/u).includes("canonical") && attrs.href !== undefined) {
      links.push({ kind: "canonical", ...attrs });
    }
  }
  return links;
}

function ids(html) {
  return new Set(
    [...html.matchAll(/\bid\s*=\s*(?:"([^"]*)"|'([^']*)')/gu)].map((m) => m[1] ?? m[2]),
  );
}

function listHtml(root) {
  return fs
    .readdirSync(root, { recursive: true, withFileTypes: true })
    .filter((entry) => entry.isFile() && entry.name.endsWith(".html"))
    .map((entry) => path.relative(root, path.join(entry.parentPath, entry.name)))
    .sort();
}

function pagePathFor(relativeHtml) {
  const posix = relativeHtml.split(path.sep).join("/");
  if (posix === "index.html") return "/";
  if (posix.endsWith("/index.html")) return `/${posix.slice(0, -"/index.html".length)}`;
  return `/${posix.slice(0, -".html".length)}`;
}

export function checkExternal(href) {
  let url;
  try {
    url = new URL(href);
  } catch {
    return `malformed URL`;
  }
  if (url.protocol === "mailto:") {
    return /^[^@\s]+@codeswhat\.com$/iu.test(decodeURIComponent(url.pathname))
      ? null
      : "mailto outside codeswhat.com";
  }
  if (url.protocol !== "https:") return `not https (${url.protocol})`;
  if (url.username || url.password) return "credentials in URL";
  if (PLACEHOLDER_HOSTS.test(url.hostname)) return `placeholder host ${url.hostname}`;
  if (!EXTERNAL_HOSTS.has(url.hostname)) return `unexpected host ${url.hostname}`;
  if (url.hostname === "github.com") {
    const owner = url.pathname.split("/")[1]?.toLowerCase();
    if (owner === "sponsors" || owner === "orgs") {
      const target = url.pathname.split("/")[2]?.toLowerCase();
      return OWN_GITHUB_OWNERS.has(target) ? null : `unexpected GitHub ${owner}/${target}`;
    }
    if (!owner) return "GitHub URL without an owner";
    if (!OWN_GITHUB_OWNERS.has(owner) && !THIRD_PARTY_GITHUB_OWNERS.has(owner)) {
      return `unexpected GitHub owner ${owner}`;
    }
  }
  return null;
}

export function checkSitemap(outputRoot) {
  const problems = [];
  const sitemapFile = path.join(outputRoot, "sitemap.xml");
  if (!isFile(sitemapFile)) return ["sitemap.xml was not exported"];
  const locs = parseSitemapLocs(fs.readFileSync(sitemapFile, "utf8"));
  if (locs.length === 0) problems.push("sitemap.xml has no <loc> entries");
  const seen = new Set();
  for (const loc of locs) {
    if (seen.has(loc)) problems.push(`sitemap lists ${loc} twice`);
    seen.add(loc);
    let url;
    try {
      url = new URL(loc);
    } catch {
      problems.push(`sitemap entry is not a URL: ${loc}`);
      continue;
    }
    if (url.origin !== CANONICAL_ORIGIN) {
      problems.push(`sitemap entry is off the canonical origin: ${loc}`);
      continue;
    }
    if (url.search || url.hash) problems.push(`sitemap entry carries a query or fragment: ${loc}`);
    const file = routeFile(outputRoot, url.pathname);
    if (!file?.endsWith(".html")) problems.push(`sitemap entry 404s: ${loc}`);
  }
  const robotsFile = path.join(outputRoot, "robots.txt");
  if (!isFile(robotsFile)) {
    problems.push("robots.txt was not exported");
  } else {
    const sitemapLines = fs
      .readFileSync(robotsFile, "utf8")
      .split(/\r?\n/u)
      .filter((line) => /^sitemap:/iu.test(line))
      .map((line) => line.replace(/^sitemap:\s*/iu, "").trim());
    if (sitemapLines.length !== 1 || sitemapLines[0] !== `${CANONICAL_ORIGIN}/sitemap.xml`) {
      problems.push(
        `robots.txt must point at ${CANONICAL_ORIGIN}/sitemap.xml, found ${JSON.stringify(sitemapLines)}`,
      );
    }
  }
  return problems;
}

export function checkLinks(outputRoot) {
  const problems = [];
  const idCache = new Map();
  const idsFor = (file) => {
    if (!idCache.has(file)) idCache.set(file, ids(fs.readFileSync(file, "utf8")));
    return idCache.get(file);
  };
  for (const relativeHtml of listHtml(outputRoot)) {
    if (FRAMEWORK_PAGES.has(relativeHtml.split(path.sep).join("/"))) continue;
    const pagePath = pagePathFor(relativeHtml);
    const pageFile = path.join(outputRoot, relativeHtml);
    const html = fs.readFileSync(pageFile, "utf8");
    const where = `${pagePath}:`;
    for (const link of extractLinks(html)) {
      const href = link.href.trim();
      if (href === "" || href === "#") {
        problems.push(`${where} empty href`);
        continue;
      }
      if (/^[a-z][a-z\d+.-]*:/iu.test(href) || href.startsWith("//")) {
        if (/^https:\/\/portwing\.codeswhat\.com(?=[/?#]|$)/iu.test(href)) {
          // Same origin written absolutely: resolved below like an internal link.
        } else {
          const problem = href.startsWith("//") ? "protocol-relative URL" : checkExternal(href);
          if (problem) problems.push(`${where} ${href}: ${problem}`);
          if (
            link.kind === "a" &&
            link.target === "_blank" &&
            !(link.rel ?? "").toLowerCase().split(/\s+/u).includes("noopener")
          ) {
            problems.push(`${where} ${href}: target=_blank without rel=noopener`);
          }
          continue;
        }
      }
      let url;
      try {
        url = new URL(href, `${CANONICAL_ORIGIN}${pagePath === "/" ? "/" : `${pagePath}`}`);
      } catch {
        problems.push(`${where} ${href}: malformed URL`);
        continue;
      }
      if (url.origin !== CANONICAL_ORIGIN) continue;
      const file = href.startsWith("#") ? pageFile : routeFile(outputRoot, url.pathname);
      if (!file) {
        problems.push(`${where} ${href}: no exported file for ${url.pathname}`);
        continue;
      }
      // Archived docs versions canonicalize to the current page on purpose.
      const archived = /^\/docs\/v\d+\.\d+(\/|$)/u.test(pagePath);
      const selfUrl = `${CANONICAL_ORIGIN}${pagePath === "/" ? "" : pagePath}`;
      if (link.kind === "canonical" && !archived && url.href.replace(/\/$/u, "") !== selfUrl) {
        problems.push(`${where} canonical ${href} does not name this page`);
      }
      const fragment = url.hash.slice(1);
      if (fragment && file.endsWith(".html") && !idsFor(file).has(decodeURIComponent(fragment))) {
        problems.push(`${where} ${href}: no element with id "${fragment}"`);
      }
    }
  }
  return problems;
}

// llms.txt is read by crawlers that never see the HTML, so its own-origin URLs
// have to resolve too.
export function checkLlmsTxt(outputRoot) {
  const file = path.join(outputRoot, "llms.txt");
  if (!isFile(file)) return [];
  const problems = [];
  const text = fs.readFileSync(file, "utf8");
  for (const match of text.matchAll(/https:\/\/portwing\.codeswhat\.com[^\s)>\]"']*/gu)) {
    const url = new URL(match[0].replace(/[.,;:]+$/u, ""));
    const target = routeFile(outputRoot, url.pathname);
    if (!target) problems.push(`llms.txt: ${url.href} 404s`);
    else if (url.hash && target.endsWith(".html")) {
      const fragment = decodeURIComponent(url.hash.slice(1));
      if (!ids(fs.readFileSync(target, "utf8")).has(fragment)) {
        problems.push(`llms.txt: ${url.href} has no element with id "${fragment}"`);
      }
    }
  }
  return problems;
}

// Where each tracked CTA in analytics/src/contract.ts is expected to point.
// null marks the CTAs that are copy buttons rather than links. A new CTA id in
// the contract has no entry here until someone decides where it goes.
export const CTA_TARGETS = {
  install_quick: null,
  install_secure: null,
  install_native: null,
  docs_root: "/docs",
  docs_getting_started: "/docs/getting-started",
  docs_installation: "/docs/installation",
  github_org: "https://github.com/CodesWhat",
  github_repository: "https://github.com/CodesWhat/portwing",
  community_discord: "https://discord.gg/mWHCPJRzSx",
};

// Placement of every link CTA, per page, as the components render them.
export const PAGE_CTAS = {
  "index.html": [
    "docs_root",
    "docs_getting_started",
    "docs_installation",
    "github_org",
    "github_repository",
    "community_discord",
  ],
  "docs/index.html": ["docs_root", "github_org", "github_repository", "community_discord"],
};

export function checkCtas(outputRoot) {
  const problems = [];
  for (const [page, ctaIds] of Object.entries(PAGE_CTAS)) {
    const file = path.join(outputRoot, page);
    if (!isFile(file)) {
      problems.push(`${page} was not exported, so its CTAs cannot be checked`);
      continue;
    }
    const hrefs = new Set(
      extractLinks(fs.readFileSync(file, "utf8")).map((link) => link.href.replace(/\/$/u, "")),
    );
    for (const id of ctaIds) {
      const target = CTA_TARGETS[id];
      if (!hrefs.has(target)) problems.push(`${page}: no link to ${target} for CTA ${id}`);
    }
  }
  return problems;
}

export function checkSite(outputRoot) {
  if (!fs.existsSync(outputRoot)) {
    return [`${outputRoot} is missing; run npm run build first`];
  }
  return [
    ...checkSitemap(outputRoot),
    ...checkLinks(outputRoot),
    ...checkLlmsTxt(outputRoot),
    ...checkCtas(outputRoot),
  ];
}

function main() {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  const outputRoot = path.join(root, "website", "out");
  const problems = checkSite(outputRoot);
  if (problems.length > 0) {
    process.stderr.write(
      `site link contract failed:\n${problems.map((p) => `  ${p}`).join("\n")}\n`,
    );
    process.exit(1);
  }
  const pages = listHtml(outputRoot).length;
  process.stdout.write(`site link contract: ${pages} pages, sitemap, robots and links ok\n`);
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main();
