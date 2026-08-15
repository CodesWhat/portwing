import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const WORKFLOW = path.join(ROOT, ".github", "workflows", "ci-verify.yml");
const LEFTHOOK = path.join(ROOT, "lefthook.yml");
const SHARED_SHA = "01bf40b06b110946f12a49b82e407d77c6480df7";

const FIXED_SCRIPTS = new Map([
  ["go-test.sh", ["go mod verify", "go test -race", "COVERAGE_MIN"]],
  [
    "go-lint.sh",
    ["golangci-lint/v2/cmd/golangci-lint@v2.12.2", "GOLANGCI_LINT_CACHE", "mktemp -d"],
  ],
  ["go-govulncheck.sh", ["golang.org/x/vuln/cmd/govulncheck@v1.2.0"]],
  ["commit-message.sh", ["validate-commit-range-test.sh", "validate-commit-range.sh"]],
  ["go-release-check.sh", ["goreleaser/goreleaser/v2@v2.17.1", "package-release-config-test.sh"]],
  ["go-qlty.sh", ["qlty-check-gate.sh all"]],
  ["go-fuzz.sh", ["FUZZER", "PKG", "fuzztime=60s"]],
  ["node-test.sh", ["npm ci", "npm run check:web"]],
]);

const FUZZERS = [
  ["FuzzParsePHC", "./internal/server/"],
  ["FuzzParseTrustedProxies", "./internal/server/"],
  ["FuzzParseImageRef", "./internal/adapter/"],
  ["FuzzParseLabels", "./internal/adapter/drydock/"],
  ["FuzzMCPHandler", "./internal/mcp/"],
  ["FuzzEnvelope", "./internal/protocol/"],
  ["FuzzVerifyRequest", "./internal/auth/"],
];

const BRIDGES = new Map([
  ["legacy-build", "Build & Test"],
  ["legacy-lint", "Lint"],
  ["legacy-govulncheck", "Govulncheck"],
  ["legacy-workflow-security", "Workflow Security"],
  ["legacy-commit-message", "Commit Message"],
  ["legacy-goreleaser", "GoReleaser Config"],
]);

const GO_PROXY_STORAGE_INPUTS = ["lint-allowed-endpoints", "goreleaser-allowed-endpoints"];

function jobSection(source, jobId) {
  const lines = source.split("\n");
  const start = lines.indexOf(`  ${jobId}:`);
  if (start === -1) return "";
  let end = lines.length;
  for (let index = start + 1; index < lines.length; index += 1) {
    if (/^ {2}[a-z][a-z0-9-]*:$/u.test(lines[index])) {
      end = index;
      break;
    }
  }
  return lines.slice(start, end).join("\n");
}

function inputBlock(source, inputName) {
  const lines = source.split("\n");
  const start = lines.indexOf(`      ${inputName}: >-`);
  if (start === -1) return "";
  let end = lines.length;
  for (let index = start + 1; index < lines.length; index += 1) {
    if (/^ {6}[a-z][a-z0-9-]*:/u.test(lines[index])) {
      end = index;
      break;
    }
  }
  return lines.slice(start, end).join("\n");
}

function assertReusableCaller(source) {
  const go = jobSection(source, "go-ci");
  const node = jobSection(source, "node-ci");
  assert.ok(go, "missing go-ci reusable caller");
  assert.ok(node, "missing node-ci reusable caller");
  assert.match(
    go,
    new RegExp(`uses: CodesWhat/\\.github/\\.github/workflows/go-ci\\.yml@${SHARED_SHA}`),
  );
  assert.match(
    node,
    new RegExp(`uses: CodesWhat/\\.github/\\.github/workflows/node-ci\\.yml@${SHARED_SHA}`),
  );
  assert.doesNotMatch(
    source,
    /CodesWhat\/\.github\/\.github\/workflows\/[^\s@]+@(?![0-9a-f]{40}\b)/u,
  );
  assert.doesNotMatch(source, /secrets:\s*inherit/u);

  for (const inputName of GO_PROXY_STORAGE_INPUTS) {
    const endpoints = inputBlock(go, inputName);
    assert.ok(endpoints, `go-ci is missing ${inputName}`);
    assert.match(
      endpoints,
      /^ {8}storage[.]googleapis[.]com:443$/mu,
      `${inputName} must permit the Go module proxy's storage redirect`,
    );
  }

  for (const input of [
    "test-check-name: Build & Test",
    "lint-check-name: Lint",
    "run-govulncheck: true",
    "run-workflow-security: true",
    "run-commit-message: true",
    `run-goreleaser: \${{ github.event_name != 'schedule' }}`,
    `run-qlty: \${{ github.event_name != 'schedule' }}`,
  ]) {
    assert.ok(go.includes(input), `go-ci is missing ${input}`);
  }
  assert.match(
    go,
    /^ {6}security-events: write$/mu,
    "go-ci must grant the nested CodeQL job's statically validated permission",
  );
  assert.doesNotMatch(go, /run-codeql:\s*true/u);
  assert.ok(jobSection(source, "codeql"), "CodeQL must remain local to preserve its category");
  assert.ok(jobSection(source, "dependency-review"), "dependency review must remain local");

  const inventory = go.match(/&& '([^'\n]+)' \|\| '\[\]'/u)?.[1];
  assert.ok(inventory, "go-ci must keep its conditional fuzz inventory in the caller");
  assert.deepEqual(
    JSON.parse(inventory),
    FUZZERS.map(([name, pkg]) => ({ name, pkg })),
  );

  for (const retired of [
    "web",
    "build",
    "lint",
    "vuln",
    "zizmor",
    "commit-message",
    "goreleaser-check",
    "go-fuzz",
    "qlty",
  ]) {
    assert.equal(jobSection(source, retired), "", `${retired} must move behind a reusable caller`);
  }
}

function assertFixedScripts() {
  for (const [name, markers] of FIXED_SCRIPTS) {
    const scriptPath = path.join(ROOT, "scripts", "ci", name);
    assert.ok(fs.existsSync(scriptPath), `missing fixed script scripts/ci/${name}`);
    const stat = fs.statSync(scriptPath);
    assert.ok((stat.mode & 0o111) !== 0, `scripts/ci/${name} must be executable`);
    const source = fs.readFileSync(scriptPath, "utf8");
    assert.match(source, /^#!\/usr\/bin\/env bash\nset -euo pipefail\n/u);
    for (const marker of markers) {
      assert.ok(source.includes(marker), `scripts/ci/${name} is missing ${marker}`);
    }
    assert.doesNotMatch(source, /\beval\b/u, `scripts/ci/${name} must not evaluate caller text`);
  }

  const fuzzScript = fs.readFileSync(path.join(ROOT, "scripts", "ci", "go-fuzz.sh"), "utf8");
  for (const [name] of FUZZERS) {
    assert.ok(!fuzzScript.includes(name), `${name} belongs in the caller, not the fixed runner`);
  }
}

function assertTemporaryBridges(source) {
  for (const [jobId, checkName] of BRIDGES) {
    const bridge = jobSection(source, jobId);
    assert.ok(bridge, `missing temporary ${checkName} bridge`);
    assert.ok(bridge.includes(`name: "${checkName}"`), `${jobId} has the wrong check name`);
    assert.match(bridge, /^ {4}needs: go-ci$/mu);
    assert.match(bridge, /^ {4}if: \$\{\{ always\(\) \}\}$/mu);
    assert.match(bridge, /test "\$\{\{ needs\.go-ci\.result \}\}" = "success"/u);
  }
}

test("Portwing calls the reusable workflows at the frozen organization SHA", () => {
  assertReusableCaller(fs.readFileSync(WORKFLOW, "utf8"));
});

test("reusable jobs invoke fixed repository-owned scripts", () => {
  assertFixedScripts();
});

test("the local lint gate uses the same isolated fixed adapter", () => {
  const lefthook = fs.readFileSync(LEFTHOOK, "utf8");
  assert.match(lefthook, /^ {4}go-lint:\n {6}run: \.\/scripts\/ci\/go-lint\.sh$/mu);
  assert.doesNotMatch(lefthook, /^ {6}run: golangci-lint run$/mu);
});

test("temporary bridges keep every legacy protected context fail-closed", () => {
  assertTemporaryBridges(fs.readFileSync(WORKFLOW, "utf8"));
});

test("the contract rejects a moving reusable ref and a fail-open bridge", () => {
  const source = fs.readFileSync(WORKFLOW, "utf8");
  assert.throws(() => assertReusableCaller(source.replaceAll(SHARED_SHA, "main")));
  const go = jobSection(source, "go-ci");
  for (const inputName of GO_PROXY_STORAGE_INPUTS) {
    const endpoints = inputBlock(go, inputName);
    assert.throws(() =>
      assertReusableCaller(
        source.replace(endpoints, endpoints.replace("        storage.googleapis.com:443\n", "")),
      ),
    );
  }
  assert.throws(() =>
    assertTemporaryBridges(
      source.replace(
        `test "\${{ needs.go-ci.result }}" = "success"`,
        `test "\${{ needs.go-ci.result }}" != "cancelled"`,
      ),
    ),
  );
});
