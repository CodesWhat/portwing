import fs from "node:fs";
import http from "node:http";
import path from "node:path";
import process from "node:process";
import { fileURLToPath, pathToFileURL } from "node:url";
import { launch } from "chrome-launcher";
import lighthouse from "lighthouse";

const SCRIPT_FILE = fileURLToPath(import.meta.url);
const ROOT = path.resolve(path.dirname(SCRIPT_FILE), "..");

export function median(values) {
  if (values.length === 0) throw new Error("median requires at least one value");
  const sorted = [...values].sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 === 0 ? (sorted[middle - 1] + sorted[middle]) / 2 : sorted[middle];
}

export function verifyLighthouseRuns(config, runs) {
  if (runs.length === 0) throw new Error(`expected Lighthouse runs for ${config.site}`);
  const result = {
    performance: median(runs.map((run) => run.performance)),
    totalByteWeight: median(runs.map((run) => run.totalByteWeight)),
    scriptTransferBytes: median(runs.map((run) => run.scriptTransferBytes)),
  };
  if (result.performance < config.performanceMin) {
    throw new Error(
      `${config.site} performance budget exceeded: ${result.performance} < ${config.performanceMin}`,
    );
  }
  if (result.totalByteWeight > config.totalByteWeightMax) {
    throw new Error(
      `${config.site} total byte weight budget exceeded: ${result.totalByteWeight} > ${config.totalByteWeightMax}`,
    );
  }
  if (result.scriptTransferBytes > config.scriptTransferBytesMax) {
    throw new Error(
      `${config.site} script transfer budget exceeded: ${result.scriptTransferBytes} > ${config.scriptTransferBytesMax}`,
    );
  }
  return result;
}

function serveStatic(outputRoot) {
  const rootPrefix = `${path.resolve(outputRoot)}${path.sep}`;
  const server = http.createServer((request, response) => {
    let pathname;
    try {
      pathname = decodeURIComponent(new URL(request.url ?? "/", "http://127.0.0.1").pathname);
    } catch {
      response.writeHead(400).end("bad request");
      return;
    }
    let file = path.resolve(outputRoot, `.${pathname}`);
    if (file !== path.resolve(outputRoot) && !file.startsWith(rootPrefix)) {
      response.writeHead(403).end("forbidden");
      return;
    }
    if (pathname.endsWith("/")) file = path.join(file, "index.html");
    if (!fs.existsSync(file) || !fs.statSync(file).isFile()) {
      response.writeHead(404).end("not found");
      return;
    }
    const contentTypes = {
      ".css": "text/css",
      ".html": "text/html",
      ".ico": "image/x-icon",
      ".js": "text/javascript",
      ".json": "application/json",
      ".png": "image/png",
      ".svg": "image/svg+xml",
      ".webmanifest": "application/manifest+json",
      ".woff2": "font/woff2",
    };
    response.writeHead(200, {
      "Content-Type": contentTypes[path.extname(file)] ?? "application/octet-stream",
      "Cache-Control": "no-store",
    });
    fs.createReadStream(file).pipe(response);
  });
  return new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => resolve(server));
  });
}

function metrics(lhr) {
  if (lhr.runtimeError?.code) throw new Error(`Lighthouse runtime error: ${lhr.runtimeError.code}`);
  const performance = lhr.categories.performance?.score;
  const totalByteWeight = lhr.audits["total-byte-weight"]?.numericValue;
  const scripts = lhr.audits["resource-summary"]?.details?.items?.find(
    (item) => item.resourceType === "script",
  );
  if (
    typeof performance !== "number" ||
    typeof totalByteWeight !== "number" ||
    typeof scripts?.transferSize !== "number"
  ) {
    throw new Error("Lighthouse report is missing required performance metrics");
  }
  return { performance, totalByteWeight, scriptTransferBytes: scripts.transferSize };
}

async function run(configPath) {
  const config = (await import(pathToFileURL(path.resolve(configPath)).href)).default;
  const outputRoot = path.join(ROOT, "website", "out");
  if (!fs.existsSync(outputRoot))
    throw new Error("website/out is missing; run npm run build first");
  const server = await serveStatic(outputRoot);
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("static server has no TCP port");
  const chrome = await launch({ chromeFlags: ["--headless", "--no-sandbox", "--disable-gpu"] });
  const outputDir = path.join(ROOT, ".lighthouseci", config.site);
  fs.rmSync(outputDir, { recursive: true, force: true });
  fs.mkdirSync(outputDir, { recursive: true });
  const url = config.url.replace("{PORT}", String(address.port));
  const runs = [];
  try {
    for (let index = 0; index < config.numberOfRuns; index += 1) {
      const result = await lighthouse(url, {
        port: chrome.port,
        output: "json",
        logLevel: "error",
        onlyCategories: ["performance"],
      });
      if (!result) throw new Error(`Lighthouse returned no result for run ${index + 1}`);
      fs.writeFileSync(path.join(outputDir, `run-${index + 1}.json`), JSON.stringify(result.lhr));
      runs.push(metrics(result.lhr));
    }
  } finally {
    await chrome.kill();
    await new Promise((resolve) => server.close(resolve));
  }
  const verified = verifyLighthouseRuns(config, runs);
  process.stdout.write(
    `${config.site}: five-run median performance=${verified.performance.toFixed(2)}, total=${verified.totalByteWeight}, scripts=${verified.scriptTransferBytes}\n`,
  );
}

if (process.argv[1] && path.resolve(process.argv[1]) === SCRIPT_FILE) {
  const configPath = process.argv[2];
  if (!configPath) throw new Error("usage: node scripts/lighthouse-budget.mjs <config.cjs>");
  await run(configPath);
}
