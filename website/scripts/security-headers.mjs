import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const SCRIPT_FILE = fileURLToPath(import.meta.url);
const PROJECT_DIR = path.resolve(path.dirname(SCRIPT_FILE), "..");
const POSTHOG_PROXY_ORIGIN = "https://e.codeswhat.com";

const COMMON_HEADERS = [
  { key: "X-Content-Type-Options", value: "nosniff" },
  { key: "X-Frame-Options", value: "DENY" },
  { key: "Referrer-Policy", value: "strict-origin-when-cross-origin" },
  {
    key: "Permissions-Policy",
    value: "camera=(), geolocation=(), microphone=(), payment=(), usb=()",
  },
];

function inlineScriptHashes(html) {
  const hashes = new Set();
  // Case-insensitive because HTML tag and attribute names are. A <SCRIPT> that
  // this missed would get no hash, and the CSP would then block a script the
  // page needs. The <img> scan below was already /gi; this one was not.
  for (const match of html.matchAll(/<script(?:\s[^>]*)?>([\s\S]*?)<\/script>/gi)) {
    if (/\ssrc=/i.test(match[0])) continue;
    const digest = crypto.createHash("sha256").update(match[1]).digest("base64");
    hashes.add(`'sha256-${digest}'`);
  }
  return [...hashes].sort();
}

function externalImageOrigins(html) {
  const origins = new Set();
  for (const match of html.matchAll(/<img\b[^>]*\bsrc=(?:"([^"]+)"|'([^']+)')[^>]*>/gi)) {
    const source = match[1] ?? match[2];
    if (!source?.startsWith("https://")) continue;
    origins.add(new URL(source).origin);
  }
  return [...origins].sort();
}

export function headersForHTML(html) {
  const scriptHashes = inlineScriptHashes(html);
  const imageOrigins = externalImageOrigins(html);
  const csp = [
    "default-src 'self'",
    `script-src 'self' ${scriptHashes.join(" ")} ${POSTHOG_PROXY_ORIGIN}`.trim(),
    "style-src 'self' 'unsafe-inline'",
    `img-src 'self' data: ${imageOrigins.join(" ")}`.trim(),
    "font-src 'self' data:",
    `connect-src 'self' ${POSTHOG_PROXY_ORIGIN}`,
    "object-src 'none'",
    "base-uri 'self'",
    "form-action 'self'",
    "frame-ancestors 'none'",
    "manifest-src 'self'",
    "worker-src 'self'",
    "upgrade-insecure-requests",
  ].join("; ");
  return [{ key: "Content-Security-Policy", value: csp }, ...COMMON_HEADERS];
}

export function routeForOutputPath(file) {
  let relative = file.replaceAll("\\", "/");
  const outMarker = relative.lastIndexOf("/out/");
  if (outMarker >= 0) relative = relative.slice(outMarker + 5);
  else if (relative.startsWith("out/")) relative = relative.slice(4);

  if (relative === "index.html") return "/";
  if (relative.endsWith("/index.html")) {
    return `/${relative.slice(0, -"/index.html".length)}`;
  }
  return `/${relative.slice(0, -".html".length)}`;
}

function htmlFiles(root) {
  const files = [];
  for (const entry of fs.readdirSync(root, { withFileTypes: true })) {
    const target = path.join(root, entry.name);
    if (entry.isDirectory()) files.push(...htmlFiles(target));
    else if (target.endsWith(".html")) files.push(target);
  }
  return files.sort();
}

function headerMap(headers) {
  return Object.fromEntries(headers.map(({ key, value }) => [key, value]));
}

function escapeRoute(route) {
  return route.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function outputRelativePath(outputDir, file) {
  return path.relative(outputDir, file).replaceAll("\\", "/");
}

export function buildOutputConfig(outputDir) {
  const routes = [];
  const overrides = {};
  for (const file of htmlFiles(outputDir)) {
    const relative = outputRelativePath(outputDir, file);
    const publicRoute = routeForOutputPath(relative);
    const csp = headerMap(headersForHTML(fs.readFileSync(file, "utf8")))["Content-Security-Policy"];
    const suffix = publicRoute === "/" ? "" : "/?";
    routes.push({
      src: `^${escapeRoute(publicRoute)}${suffix}$`,
      headers: { "Content-Security-Policy": csp },
      continue: true,
    });

    const fileRoute = `/${relative}`;
    if (fileRoute !== publicRoute) {
      routes.push({
        src: `^${escapeRoute(fileRoute)}$`,
        headers: { "Content-Security-Policy": csp },
        continue: true,
      });
    }

    if (publicRoute !== "/") {
      overrides[relative] = { path: publicRoute.slice(1) };
    }
  }

  routes.push({ src: "^/.*$", headers: headerMap(COMMON_HEADERS), continue: true });
  return {
    version: 3,
    routes,
    overrides,
    framework: { version: "next-static-export" },
  };
}

export function generateBuildOutput(outputDir, buildOutputDir) {
  const staticDir = path.join(buildOutputDir, "static");
  fs.rmSync(buildOutputDir, { recursive: true, force: true });
  fs.mkdirSync(buildOutputDir, { recursive: true });
  fs.cpSync(outputDir, staticDir, { recursive: true });
  fs.writeFileSync(
    path.join(buildOutputDir, "config.json"),
    `${JSON.stringify(buildOutputConfig(outputDir), null, 2)}\n`,
  );
}

function main() {
  const outputDir = path.join(PROJECT_DIR, "out");
  const buildOutputDir = path.resolve(PROJECT_DIR, "..", ".vercel", "output");
  if (!fs.existsSync(outputDir)) {
    throw new Error(`static output not found: ${outputDir}; run next build first`);
  }
  generateBuildOutput(outputDir, buildOutputDir);
}

if (process.argv[1] && path.resolve(process.argv[1]) === SCRIPT_FILE) {
  main();
}
