import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

const ROOT = path.resolve(import.meta.dirname, "..");

function read(relativePath) {
  return fs.readFileSync(path.join(ROOT, relativePath), "utf8");
}

function sourceFiles(root) {
  return fs
    .readdirSync(root, { recursive: true, withFileTypes: true })
    .filter((entry) => entry.isFile() && /\.(?:ts|tsx|js|mjs)$/.test(entry.name))
    .map((entry) => read(path.relative(ROOT, path.join(entry.parentPath, entry.name))));
}

test("both web roots use the shared PostHog client and no Vercel analytics", () => {
  const rootPackage = JSON.parse(read("package.json"));
  const lock = read("package-lock.json");
  const sources = sourceFiles(path.join(ROOT, "website", "src"))
    .concat(sourceFiles(path.join(ROOT, "docs", "src")))
    .join("\n");

  assert.ok(rootPackage.workspaces.includes("analytics"));
  assert.match(lock, /"posthog-js": "1\.417\.0"/);
  assert.doesNotMatch(lock, /"@vercel\/analytics"/);
  assert.doesNotMatch(sources, /@vercel\/analytics|SpeedInsights|\.identify\s*\(/);
  const analyticsClient = read("analytics/src/client.ts");
  assert.match(analyticsClient, /posthog-js\/dist\/module\.slim"/);
  assert.match(analyticsClient, /createWebVitalsBuffer|captureWebVital/);
  assert.doesNotMatch(analyticsClient, /WebVitalsAutocapture|extension-bundles/);

  const extensionBundleBytes = fs.statSync(
    path.join(ROOT, "node_modules", "posthog-js", "dist", "extension-bundles.js"),
  ).size;
  assert.equal(extensionBundleBytes, 138_263);
  assert.ok(extensionBundleBytes > 850_000 - 784_278);

  for (const app of ["website", "docs"]) {
    assert.match(read(`${app}/src/instrumentation-client.ts`), /initializeAnalytics/);
    assert.match(read(`${app}/src/app/layout.tsx`), /<AnalyticsRuntime\s*\/>/);
    const runtime = read(`${app}/src/components/analytics-runtime.tsx`);
    assert.match(runtime, /capturePageview\(pathname\)/);
    assert.match(runtime, /useReportWebVitals\(reportWebVital\)/);
    assert.match(runtime, /useCallback<Parameters<typeof useReportWebVitals>\[0\]>/);
    assert.match(runtime, /const webVitalsPath = useRef\(pathname\)\.current/);
    assert.match(runtime, /captureWebVital\(webVitalsPath, name, value\)/);
    assert.match(runtime, /\[webVitalsPath\]/);
    assert.doesNotMatch(runtime, /pathnameRef|\.current = pathname/);
    assert.match(runtime, /useEffect\(\(\) => \{\s*capturePageview\(pathname\)/);
  }

  assert.doesNotMatch(read("analytics/src/contract.ts"), /metric_name|metric_value/);
});

test("the finite CTA source map covers actual tracked component calls", () => {
  const contract = read("analytics/src/contract.ts");
  const trackedSources = [
    "website/src/components/cta-buttons.tsx",
    "website/src/components/footer.tsx",
    "website/src/components/get-started.tsx",
    "website/src/components/site-header.tsx",
    "website/src/components/star-history.tsx",
    "docs/src/components/footer.tsx",
    "docs/src/components/site-header.tsx",
  ].map(read);

  for (const id of [
    "install_quick",
    "install_secure",
    "install_native",
    "docs_root",
    "docs_getting_started",
    "docs_installation",
    "github_org",
    "github_repository",
    "community_discord",
  ]) {
    assert.match(contract, new RegExp(`\\b${id}\\b`));
    assert.ok(
      trackedSources.some((source) => source.includes(`"${id}"`)),
      `${id} is unused`,
    );
  }

  assert.doesNotMatch(trackedSources.join("\n"), /docs_security/);
  assert.doesNotMatch(contract, /docs_security/);
});
