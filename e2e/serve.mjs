// SPDX-License-Identifier: MIT

import fs from "node:fs";
import http from "node:http";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

import { headersForHTML } from "../website/scripts/security-headers.mjs";

const SCRIPT_FILE = fileURLToPath(import.meta.url);
const ROOT = path.resolve(path.dirname(SCRIPT_FILE), "..");
const OUT_DIR = path.join(ROOT, "website", "out");

const TYPES = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json; charset=utf-8",
  ".txt": "text/plain; charset=utf-8",
  ".xml": "application/xml; charset=utf-8",
  ".webmanifest": "application/manifest+json",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".webp": "image/webp",
  ".ico": "image/x-icon",
  ".woff2": "font/woff2",
  ".woff": "font/woff",
};

function isInside(file) {
  const relative = path.relative(OUT_DIR, file);
  return relative !== "" && !relative.startsWith("..") && !path.isAbsolute(relative);
}

// Clean URLs, the way the static output is deployed: /compare serves compare.html and
// /docs serves docs/index.html.
function fileFor(pathname) {
  let decoded;
  try {
    decoded = decodeURIComponent(pathname);
  } catch {
    return null;
  }
  const clean = decoded.replace(/\/+$/u, "") || "/";
  const base = path.join(OUT_DIR, clean);
  const candidates =
    clean === "/"
      ? [path.join(OUT_DIR, "index.html")]
      : [base, `${base}.html`, path.join(base, "index.html")];
  for (const candidate of candidates) {
    if (!isInside(candidate)) continue;
    if (fs.existsSync(candidate) && fs.statSync(candidate).isFile()) return candidate;
  }
  return null;
}

function send(response, status, file) {
  const body = fs.readFileSync(file);
  const type = TYPES[path.extname(file)] ?? "application/octet-stream";
  const isHTML = type.startsWith("text/html");
  response.setHeader("Content-Type", type);
  // The same per-page policy the production output ships, hashed from the page's own inline
  // scripts. A script or hash that drifts is blocked here exactly as it would be live.
  for (const { key, value } of headersForHTML(isHTML ? body.toString("utf8") : "")) {
    if (key === "Content-Security-Policy" && !isHTML) continue;
    // WebKit honors upgrade-insecure-requests on a plain-http loopback origin and rewrites every
    // subresource to https, which nothing here serves. It is the one directive that only means
    // something over https, so it is the one dropped; every other directive is enforced as built.
    response.setHeader(key, value.replace(/;\s*upgrade-insecure-requests/u, ""));
  }
  response.writeHead(status);
  response.end(body);
}

export function createServer() {
  return http.createServer((request, response) => {
    const { pathname } = new URL(request.url ?? "/", "http://localhost");
    const file = fileFor(pathname);
    if (file) {
      send(response, 200, file);
      return;
    }
    // A missing path is served from the error phase: 404 status, the 404 page's own CSP.
    send(response, 404, path.join(OUT_DIR, "404.html"));
  });
}

function main() {
  if (!fs.existsSync(path.join(OUT_DIR, "index.html"))) {
    throw new Error(`static output not found: ${OUT_DIR}; run npm run build first`);
  }
  const port = Number(process.env.E2E_PORT ?? 4417);
  createServer().listen(port, "127.0.0.1", () => {
    console.log(`serving ${OUT_DIR} on http://127.0.0.1:${port}`);
  });
}

if (process.argv[1] && path.resolve(process.argv[1]) === SCRIPT_FILE) {
  main();
}
