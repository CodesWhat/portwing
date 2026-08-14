import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const SCRIPT_FILE = fileURLToPath(import.meta.url);
const ROOT = path.resolve(path.dirname(SCRIPT_FILE), "..");

const BUDGETS = [
  {
    site: "marketing",
    route: "index.html",
    baselineTotalBytes: 4_412_576,
    baselineScriptBytes: 801_153,
    totalBytes: 4_550_000,
    scriptBytes: 850_000,
  },
  {
    site: "docs",
    route: "docs/index.html",
    baselineTotalBytes: 2_206_624,
    baselineScriptBytes: 958_448,
    totalBytes: 2_330_000,
    scriptBytes: 1_020_000,
  },
];

function localAssetPaths(html) {
  const assets = new Set();
  for (const match of html.matchAll(/(?:src|href)=["']([^"']+)["']/gu)) {
    const source = match[1].split(/[?#]/u, 1)[0];
    if (source.startsWith("/") && !source.startsWith("//")) assets.add(source.slice(1));
  }
  return assets;
}

export function measurePage(outputRoot, route) {
  const htmlPath = path.join(outputRoot, route);
  if (!fs.existsSync(htmlPath)) throw new Error(`missing page: ${route}`);
  const files = new Set([htmlPath]);
  for (const asset of localAssetPaths(fs.readFileSync(htmlPath, "utf8"))) {
    const assetPath = path.join(outputRoot, asset);
    if (!fs.existsSync(assetPath)) throw new Error(`missing local asset for ${route}: /${asset}`);
    files.add(assetPath);
  }
  let totalBytes = 0;
  let scriptBytes = 0;
  for (const file of files) {
    const bytes = fs.statSync(file).size;
    totalBytes += bytes;
    if (path.extname(file) === ".js") scriptBytes += bytes;
  }
  return { totalBytes, scriptBytes, assetCount: files.size };
}

export function verifyPageBudgets(outputRoot, budgets = BUDGETS) {
  return budgets.map((budget) => {
    const result = measurePage(outputRoot, budget.route);
    if (result.scriptBytes > budget.scriptBytes) {
      throw new Error(
        `${budget.site ?? budget.route} script budget exceeded: ${result.scriptBytes} > ${budget.scriptBytes}`,
      );
    }
    if (result.totalBytes > budget.totalBytes) {
      throw new Error(
        `${budget.site ?? budget.route} total budget exceeded: ${result.totalBytes} > ${budget.totalBytes}`,
      );
    }
    return { ...budget, ...result };
  });
}

function main() {
  const results = verifyPageBudgets(path.join(ROOT, "website", "out"));
  for (const result of results) {
    process.stdout.write(
      `${result.site}: ${result.totalBytes}/${result.totalBytes > result.baselineTotalBytes ? "+" : ""}${result.baselineTotalBytes} total bytes, ${result.scriptBytes}/${result.scriptBytes > result.baselineScriptBytes ? "+" : ""}${result.baselineScriptBytes} script bytes, ${result.assetCount} assets\n`,
    );
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === SCRIPT_FILE) main();
