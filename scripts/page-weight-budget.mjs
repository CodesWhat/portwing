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
    mountPath: "/",
    baselineTotalBytes: 4_513_931,
    baselineScriptBytes: 801_221,
    totalBytes: 4_655_000,
    scriptBytes: 850_000,
  },
  {
    site: "docs",
    route: "index.html",
    mountPath: "/docs",
    baselineTotalBytes: 2_342_852,
    baselineScriptBytes: 958_516,
    totalBytes: 2_475_000,
    scriptBytes: 1_020_000,
  },
];

function attribute(tag, name) {
  const match = tag.match(new RegExp(`\\b${name}=(?:"([^"]*)"|'([^']*)')`, "iu"));
  return match?.[1] ?? match?.[2] ?? null;
}

function resourceUrls(html) {
  const resources = new Set();
  for (const match of html.matchAll(
    /<(?:audio|embed|iframe|img|input|link|object|script|source|video)\b[^>]*>/giu,
  )) {
    const tag = match[0];
    const tagName = tag.match(/^<([a-z]+)/iu)?.[1]?.toLowerCase();
    if (tagName === "link") {
      const rel = attribute(tag, "rel")?.toLowerCase().split(/\s+/u) ?? [];
      if (
        !rel.some((value) =>
          ["icon", "manifest", "modulepreload", "preload", "stylesheet"].includes(value),
        )
      ) {
        continue;
      }
    }
    for (const name of ["data", "poster", "src"]) {
      const value = attribute(tag, name);
      if (value) resources.add(value);
    }
    const srcset = attribute(tag, "srcset");
    if (srcset) {
      for (const candidate of srcset.split(",")) {
        const value = candidate.trim().split(/\s+/u, 1)[0];
        if (value) resources.add(value);
      }
    }
    if (tagName === "link") {
      const href = attribute(tag, "href");
      if (href) resources.add(href);
    }
  }
  return resources;
}

function inlineCss(html) {
  const styles = [...html.matchAll(/<style\b[^>]*>([\s\S]*?)<\/style>/giu)].map(
    (match) => match[1],
  );
  for (const match of html.matchAll(/\bstyle=(?:"([^"]*)"|'([^']*)')/giu)) {
    styles.push(match[1] ?? match[2]);
  }
  return styles;
}

function cssUrls(css) {
  const resources = new Set();
  for (const match of css.matchAll(/url\(\s*(?:"([^"]+)"|'([^']+)'|([^)'"\s]+))\s*\)/giu)) {
    resources.add(match[1] ?? match[2] ?? match[3]);
  }
  for (const match of css.matchAll(/@import\s+(?:"([^"]+)"|'([^']+)')/giu)) {
    resources.add(match[1] ?? match[2]);
  }
  return resources;
}

function normalizedMountPath(mountPath) {
  if (typeof mountPath !== "string" || !mountPath.startsWith("/")) {
    throw new Error(`invalid mount path: ${mountPath}`);
  }
  const normalized = mountPath.length > 1 ? mountPath.replace(/\/+$/u, "") : mountPath;
  if (normalized.split("/").some((segment) => segment === "." || segment === "..")) {
    throw new Error(`invalid mount path: ${mountPath}`);
  }
  return normalized;
}

function insideRoot(candidate, root) {
  return candidate === root || candidate.startsWith(`${root}${path.sep}`);
}

function resolveLocalAsset(outputRoot, sourceFile, rawUrl, options) {
  const { mountPath, rootOutputRoot } = options;
  const source = rawUrl.split(/[?#]/u, 1)[0];
  if (!source || source.startsWith("//") || /^[a-z][a-z\d+.-]*:/iu.test(source)) return null;
  let decoded;
  try {
    decoded = decodeURIComponent(source);
  } catch {
    throw new Error(
      `invalid local asset URL in ${path.relative(outputRoot, sourceFile)}: ${rawUrl}`,
    );
  }
  let mountedSource = decoded;
  let assetRoot = outputRoot;
  if (decoded.startsWith("/")) {
    const normalizedMount = normalizedMountPath(mountPath);
    if (normalizedMount !== "/") {
      if (decoded === normalizedMount || decoded.startsWith(`${normalizedMount}/`)) {
        mountedSource = decoded.slice(normalizedMount.length) || "/";
      } else {
        assetRoot = rootOutputRoot;
      }
    }
  }
  const candidate = mountedSource.startsWith("/")
    ? path.resolve(assetRoot, `.${mountedSource}`)
    : path.resolve(path.dirname(sourceFile), decoded);
  const allowedRoots = [path.resolve(outputRoot), path.resolve(rootOutputRoot)];
  if (!allowedRoots.some((root) => insideRoot(candidate, root))) {
    throw new Error(`local asset escapes output roots: ${rawUrl}`);
  }
  return candidate;
}

// Size and contents both come from one open descriptor. Checking a path and
// then acting on it is two lookups that can disagree, and here they would
// disagree for a mundane reason rather than a hostile one: a build writing into
// the output tree while the budget walks it would report a size that belongs to
// a different file than the one it went on to read. The descriptor pins one
// inode for both. Returns null when the path is not a readable regular file, so
// the caller decides what a miss means.
function readAsset(target, { text = false } = {}) {
  let fd;
  try {
    fd = fs.openSync(target, "r");
  } catch (error) {
    if (error.code === "ENOENT" || error.code === "ENOTDIR" || error.code === "EACCES") return null;
    throw error;
  }
  try {
    const stat = fs.fstatSync(fd);
    if (!stat.isFile()) return null;
    return { size: stat.size, text: text ? fs.readFileSync(fd, "utf8") : null };
  } finally {
    fs.closeSync(fd);
  }
}

export function measurePage(
  outputRoot,
  route,
  { mountPath = "/", rootOutputRoot = outputRoot } = {},
) {
  const htmlPath = path.join(outputRoot, route);
  const page = readAsset(htmlPath, { text: true });
  if (!page) throw new Error(`missing page: ${route}`);
  const sizes = new Map([[htmlPath, page.size]]);
  const html = page.text;
  const pending = [...resourceUrls(html)].map((url) => ({ sourceFile: htmlPath, url }));
  for (const css of inlineCss(html)) {
    pending.push(...[...cssUrls(css)].map((url) => ({ sourceFile: htmlPath, url })));
  }
  while (pending.length > 0) {
    const { sourceFile, url } = pending.pop();
    const assetPath = resolveLocalAsset(outputRoot, sourceFile, url, {
      mountPath,
      rootOutputRoot,
    });
    if (!assetPath || sizes.has(assetPath)) continue;
    const isCss = path.extname(assetPath).toLowerCase() === ".css";
    const asset = readAsset(assetPath, { text: isCss });
    if (!asset) throw new Error(`missing local asset for ${route}: ${url}`);
    sizes.set(assetPath, asset.size);
    if (isCss) {
      pending.push(
        ...[...cssUrls(asset.text)].map((nestedUrl) => ({ sourceFile: assetPath, url: nestedUrl })),
      );
    }
  }
  let totalBytes = 0;
  let scriptBytes = 0;
  for (const [file, bytes] of sizes) {
    totalBytes += bytes;
    if (path.extname(file) === ".js") scriptBytes += bytes;
  }
  return { totalBytes, scriptBytes, assetCount: sizes.size };
}

export function verifyPageBudgets(outputRoot, budgets = BUDGETS, options = {}) {
  return budgets.map((budget) => {
    const result = measurePage(outputRoot, budget.route, {
      ...options,
      mountPath: budget.mountPath,
    });
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

export function verifyProductionPageBudgets(root = ROOT) {
  return [
    ...verifyPageBudgets(
      path.join(root, "website", "out"),
      BUDGETS.filter((budget) => budget.site === "marketing"),
    ),
    ...verifyPageBudgets(
      path.join(root, "docs", "out"),
      BUDGETS.filter((budget) => budget.site === "docs"),
      { rootOutputRoot: path.join(root, "website", "out") },
    ),
  ];
}

function main() {
  const results = verifyProductionPageBudgets();
  for (const result of results) {
    process.stdout.write(
      `${result.site}: ${result.totalBytes}/${result.totalBytes > result.baselineTotalBytes ? "+" : ""}${result.baselineTotalBytes} total bytes, ${result.scriptBytes}/${result.scriptBytes > result.baselineScriptBytes ? "+" : ""}${result.baselineScriptBytes} script bytes, ${result.assetCount} assets\n`,
    );
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === SCRIPT_FILE) main();
