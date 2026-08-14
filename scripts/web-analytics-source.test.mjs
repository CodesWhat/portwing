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
  assert.match(read("analytics/src/client.ts"), /posthog-js\/dist\/module\.slim\.no-external/);

  for (const app of ["website", "docs"]) {
    assert.match(read(`${app}/src/instrumentation-client.ts`), /initializeAnalytics/);
    assert.match(read(`${app}/src/app/layout.tsx`), /<AnalyticsRuntime\s*\/>/);
  }
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
});
