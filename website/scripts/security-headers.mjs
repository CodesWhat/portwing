import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const SCRIPT_FILE = fileURLToPath(import.meta.url);
const PROJECT_DIR = path.resolve(path.dirname(SCRIPT_FILE), "..");

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
  for (const match of html.matchAll(/<script(?:\s[^>]*)?>([\s\S]*?)<\/script>/g)) {
    if (/\ssrc=/.test(match[0])) continue;
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
    `script-src 'self' ${scriptHashes.join(" ")}`.trim(),
    "style-src 'self' 'unsafe-inline'",
    `img-src 'self' data: ${imageOrigins.join(" ")}`.trim(),
    "font-src 'self' data:",
    "connect-src 'self'",
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

function generateConfig(outputDir) {
  const pageHeaders = htmlFiles(outputDir).map((file) => ({
    source: routeForOutputPath(file),
    headers: headersForHTML(fs.readFileSync(file, "utf8")).filter(
      ({ key }) => key === "Content-Security-Policy",
    ),
  }));
  return {
    headers: [{ source: "/(.*)", headers: COMMON_HEADERS }, ...pageHeaders],
  };
}

function main() {
  const outputDir = path.join(PROJECT_DIR, "out");
  const configPath = path.join(PROJECT_DIR, "vercel.json");
  if (!fs.existsSync(outputDir)) {
    throw new Error(`static output not found: ${outputDir}; run next build first`);
  }
  const generated = `${JSON.stringify(generateConfig(outputDir), null, 2)}\n`;
  if (process.argv.includes("--check")) {
    const current = fs.existsSync(configPath) ? fs.readFileSync(configPath, "utf8") : "";
    if (current !== generated) {
      throw new Error("vercel.json security headers are stale; run node scripts/security-headers.mjs");
    }
    return;
  }
  fs.writeFileSync(configPath, generated);
}

if (process.argv[1] && path.resolve(process.argv[1]) === SCRIPT_FILE) {
  main();
}
