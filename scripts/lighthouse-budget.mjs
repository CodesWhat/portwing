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

export function verifyBrowserConsole(config, items) {
  const errors = items.filter((item) => item.source !== "network");
  if (errors.length > 0) {
    throw new Error(`${config.site} logged a browser console error: ${errors[0].description}`);
  }
}

function serveStatic(outputRoot, mountPath) {
  const rootPrefix = `${path.resolve(outputRoot)}${path.sep}`;
  const mountPrefix = mountPath === "/" ? "" : mountPath;
  const server = http.createServer((request, response) => {
    let requestURL;
    let pathname;
    try {
      requestURL = new URL(request.url ?? "/", "http://127.0.0.1");
      pathname = decodeURIComponent(requestURL.pathname);
    } catch {
      response.writeHead(400).end("bad request");
      return;
    }
    if (mountPrefix && pathname !== mountPrefix && !pathname.startsWith(`${mountPrefix}/`)) {
      response.writeHead(404).end("not found");
      return;
    }
    if (pathname.endsWith(".html")) {
      const cleanPath = pathname.endsWith("/index.html")
        ? pathname.slice(0, -"/index.html".length) || "/"
        : pathname.slice(0, -".html".length);
      response.writeHead(308, { Location: `${cleanPath}${requestURL.search}` }).end();
      return;
    }
    const outputPathname = mountPrefix ? pathname.slice(mountPrefix.length) || "/" : pathname;
    let file = path.resolve(outputRoot, `.${outputPathname}`);
    if (file !== path.resolve(outputRoot) && !file.startsWith(rootPrefix)) {
      response.writeHead(403).end("forbidden");
      return;
    }
    if (pathname.endsWith("/")) {
      file = path.join(file, "index.html");
    } else if (fs.existsSync(`${file}.html`)) {
      file = `${file}.html`;
    } else if (fs.existsSync(file) && fs.statSync(file).isDirectory()) {
      file = path.join(file, "index.html");
    }
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

export async function withLighthouseResources({ startServer, startChrome, run }) {
  const server = await startServer();
  let chrome;
  try {
    chrome = await startChrome();
    return await run({ server, chrome });
  } finally {
    try {
      if (chrome) await chrome.kill();
    } finally {
      await new Promise((resolve) => server.close(resolve));
    }
  }
}

async function run(configPath) {
  const config = (await import(pathToFileURL(path.resolve(configPath)).href)).default;
  if (typeof config.outputRoot !== "string" || config.outputRoot.length === 0) {
    throw new Error(`Lighthouse output root is missing for ${config.site ?? configPath}`);
  }
  if (
    typeof config.mountPath !== "string" ||
    !config.mountPath.startsWith("/") ||
    (config.mountPath !== "/" && config.mountPath.endsWith("/"))
  ) {
    throw new Error(`Lighthouse mount path is invalid for ${config.site ?? configPath}`);
  }
  const outputRoot = path.resolve(ROOT, config.outputRoot);
  if (!fs.existsSync(outputRoot))
    throw new Error(`${config.outputRoot} is missing; run npm run build first`);
  const runs = await withLighthouseResources({
    startServer: () => serveStatic(outputRoot, config.mountPath),
    startChrome: () => launch({ chromeFlags: ["--headless", "--no-sandbox", "--disable-gpu"] }),
    run: async ({ server, chrome }) => {
      const address = server.address();
      if (!address || typeof address === "string") {
        throw new Error("static server has no TCP port");
      }
      const outputDir = path.join(ROOT, ".lighthouseci", config.site);
      fs.rmSync(outputDir, { recursive: true, force: true });
      fs.mkdirSync(outputDir, { recursive: true });
      const url = config.url.replace("{PORT}", String(address.port));
      const completedRuns = [];
      for (let index = 0; index < config.numberOfRuns; index += 1) {
        const result = await lighthouse(url, {
          port: chrome.port,
          output: "json",
          logLevel: "error",
          onlyCategories: ["performance"],
        });
        if (!result) throw new Error(`Lighthouse returned no result for run ${index + 1}`);
        fs.writeFileSync(path.join(outputDir, `run-${index + 1}.json`), JSON.stringify(result.lhr));
        completedRuns.push(metrics(result.lhr));
      }
      if (config.consoleRoute) {
        const consoleUrl = new URL(config.consoleRoute, url).href;
        const result = await lighthouse(consoleUrl, {
          port: chrome.port,
          output: "json",
          logLevel: "error",
          onlyAudits: ["errors-in-console"],
        });
        if (!result) throw new Error("Lighthouse returned no browser console result");
        const items = result.lhr.audits["errors-in-console"]?.details?.items;
        if (!Array.isArray(items)) throw new Error("Lighthouse browser console audit is missing");
        verifyBrowserConsole(config, items);
      }
      return completedRuns;
    },
  });
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
