import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const ROOT = path.resolve(import.meta.dirname, "..");

function fixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "portwing-ci-adapters-"));
  const bin = path.join(root, "bin");
  fs.mkdirSync(bin);
  const go = path.join(bin, "go");
  fs.writeFileSync(
    go,
    `#!/usr/bin/env bash
set -u
case "\${1:-}" in
mod|vet|build) exit 0 ;;
list)
  case "\${GO_LIST_FIXTURE:-failure}" in
  failure) echo "go list fixture failed" >&2; exit 37 ;;
  empty) exit 0 ;;
  success) printf '%s\\n' example.invalid/internal/auth example.invalid/internal/banner/gen ;;
  esac
  ;;
test)
  count=0
  if [ -f "\${MOCK_GO_STATE:-}" ]; then count="$(cat "\${MOCK_GO_STATE}")"; fi
  count=$((count + 1))
  printf '%s' "\${count}" >"\${MOCK_GO_STATE}"
  if [ "\${count}" -eq 2 ] && [ "\${MOCK_FUZZ_SECOND_FAILURE:-}" = "non-flake" ]; then
    echo "attempt-2: fatal fixture failure"
    exit 43
  fi
  echo "attempt-\${count}: context deadline exceeded"
  exit 42
  ;;
tool) echo 'total: (statements) 100.0%' ;;
*) echo "unexpected go command: $*" >&2; exit 90 ;;
esac
`,
  );
  fs.chmodSync(go, 0o755);
  return { root, bin };
}

function run(script, root, bin, env = {}) {
  return spawnSync("/bin/bash", [path.join(ROOT, "scripts", "ci", script)], {
    cwd: root,
    encoding: "utf8",
    env: { ...process.env, PATH: `${bin}:${process.env.PATH}`, ...env },
  });
}

test("Go test adapter preserves go list failures and rejects an empty package set", (t) => {
  const { root, bin } = fixture();
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));

  const failure = run("go-test.sh", root, bin, { GO_LIST_FIXTURE: "failure" });
  assert.equal(failure.status, 37);
  assert.match(failure.stderr, /go list fixture failed/u);
  assert.doesNotMatch(failure.stderr, /unbound variable/u);

  const empty = run("go-test.sh", root, bin, { GO_LIST_FIXTURE: "empty" });
  assert.equal(empty.status, 1);
  assert.match(empty.stderr, /go list returned no testable packages/u);
  assert.doesNotMatch(empty.stderr, /unbound variable/u);
});

test("Go fuzz adapter preserves the log and corpus from both bounded attempts", (t) => {
  const { root, bin } = fixture();
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const fuzzer = "FuzzFixture";
  const corpus = path.join(root, "internal", "fixture", "testdata", "fuzz", fuzzer);
  fs.mkdirSync(corpus, { recursive: true });
  fs.writeFileSync(path.join(corpus, "seed"), "seed");
  const state = path.join(root, "attempt-count");

  const result = run("go-fuzz.sh", root, bin, {
    FUZZER: fuzzer,
    PKG: "./internal/fixture/",
    MOCK_GO_STATE: state,
  });
  assert.equal(result.status, 1);
  const artifacts = path.join(root, "artifacts", "go-fuzz", fuzzer);
  const log = fs.readFileSync(path.join(artifacts, "fuzz.log"), "utf8");
  assert.match(log, /attempt-1: context deadline exceeded/u);
  assert.match(log, /attempt-2: context deadline exceeded/u);
  for (const attempt of [1, 2]) {
    assert.equal(
      fs.readFileSync(path.join(artifacts, `corpus-attempt-${attempt}`, "seed"), "utf8"),
      "seed",
    );
  }
});

test("Go fuzz retry classifies only the current attempt output", (t) => {
  const { root, bin } = fixture();
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const result = run("go-fuzz.sh", root, bin, {
    FUZZER: "FuzzFixture",
    PKG: "./internal/fixture/",
    MOCK_GO_STATE: path.join(root, "attempt-count"),
    MOCK_FUZZ_SECOND_FAILURE: "non-flake",
  });
  assert.equal(result.status, 43);
  assert.match(result.stderr, /failed for a non-flake reason \(exit 43\)/u);
});

test("Go fuzz adapter rejects package paths that escape the module", (t) => {
  const { root, bin } = fixture();
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));

  for (const packagePath of ["./../other-module", "./internal/../../other-module"]) {
    const result = run("go-fuzz.sh", root, bin, {
      FUZZER: "FuzzFixture",
      PKG: packagePath,
    });
    assert.equal(result.status, 2, packagePath);
    assert.match(result.stderr, /invalid PKG/u);
  }
});
