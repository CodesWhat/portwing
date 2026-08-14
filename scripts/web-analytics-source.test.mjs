import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
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
  assert.doesNotMatch(analyticsClient, /no-external|captureWebVital|buildWebVitalsEvent/);

  const loaderProbe = execFileSync(
    process.execPath,
    [
      "-e",
      [
        "global.window=global;",
        'const posthog=require("posthog-js/dist/module.slim").default;',
        "let request;",
        "global.__PosthogExtensions__.loadExternalDependency(",
        "{version:posthog.version,config:{disable_external_dependency_loading:false},",
        "requestRouter:{endpointFor:(type,path)=>(request={type,path,url:'https://e.codeswhat.com'+path}).url}},",
        '"web-vitals",()=>{});',
        "process.stdout.write(JSON.stringify({",
        "loader:typeof global.__PosthogExtensions__?.loadExternalDependency,request}));",
      ].join(""),
    ],
    { cwd: path.join(ROOT, "analytics"), encoding: "utf8" },
  );
  assert.deepEqual(JSON.parse(loaderProbe), {
    loader: "function",
    request: {
      type: "assets",
      path: "/static/web-vitals.js?v=1.417.0",
      url: "https://e.codeswhat.com/static/web-vitals.js?v=1.417.0",
    },
  });

  const bufferedCapture = execFileSync(
    process.execPath,
    [
      "-e",
      [
        'const {WebVitalsAutocapture}=require("posthog-js/lib/src/extensions/web-vitals/index.js");',
        "const captures=[];",
        "const vitals=new WebVitalsAutocapture({",
        "config:{capture_performance:{web_vitals:true,",
        'web_vitals_allowed_metrics:["CLS","FCP","INP","LCP"],',
        "web_vitals_attribution:false},disable_capture_url_hashes:true},",
        "persistence:{props:{}},capture:(event,properties)=>captures.push({event,properties})});",
        'for(const [name,value] of [["CLS",0.01],["FCP",123],["INP",45],["LCP",456]])',
        'vitals._addToBuffer({name,value,navigationURL:"https://portwing.codeswhat.com/docs",navigationId:1});',
        "process.stdout.write(JSON.stringify(captures));",
      ].join(""),
    ],
    { cwd: ROOT, encoding: "utf8" },
  );
  const captures = JSON.parse(bufferedCapture);
  assert.equal(captures.length, 1);
  assert.equal(captures[0].event, "$web_vitals");
  assert.deepEqual(
    Object.keys(captures[0].properties).filter((key) => key.endsWith("_value")),
    [
      "$web_vitals_CLS_value",
      "$web_vitals_FCP_value",
      "$web_vitals_INP_value",
      "$web_vitals_LCP_value",
    ],
  );

  for (const app of ["website", "docs"]) {
    assert.match(read(`${app}/src/instrumentation-client.ts`), /initializeAnalytics/);
    assert.match(read(`${app}/src/app/layout.tsx`), /<AnalyticsRuntime\s*\/>/);
    const runtime = read(`${app}/src/components/analytics-runtime.tsx`);
    assert.match(runtime, /capturePageview\(pathname\)/);
    assert.doesNotMatch(runtime, /useReportWebVitals|captureWebVital|useCallback|useRef/);
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
});
