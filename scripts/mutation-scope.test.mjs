// SPDX-License-Identifier: MIT

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

const workflow = fs.readFileSync(
  new URL("../.github/workflows/quality-mutation-monthly.yml", import.meta.url),
  "utf8",
);

function runGatingStep(pkg, baselineFails = false, advisory = false) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "pw-mutation-scope-"));
  try {
    fs.mkdirSync(path.join(dir, "scripts/ci"), { recursive: true });
    fs.mkdirSync(path.join(dir, "bin"));
    for (const [name, body] of [
      ["go", 'echo go >> "$EVENTS"; printf "%s\\n" "$@" > "$GO_ARGS"; exit "$BASELINE_STATUS"'],
      [
        "gremlins",
        `if [ "\${2:-}" = --help ]; then
  printf '%s\\n' ' --invert-logical ' ' --invert-bitwise ' ' --invert-bwassign ' ' --invert-assignments ' ' --invert-loopctrl ' ' --remove-self-assignments '
  exit 0
fi
echo gremlins >> "$EVENTS"
printf '%s\\n' "$@" > "$GREMLINS_ARGS"
while [ "$#" -gt 0 ]; do
  if [ "$1" = --output ]; then
    printf '%s\\n' '{"test_efficacy":100,"mutants_killed":1,"mutator_statistics":{"invert_logical":1}}' > "$2"
    break
  fi
  shift
done`,
      ],
    ]) {
      fs.writeFileSync(path.join(dir, "bin", name), `#!/bin/bash\n${body}\n`, { mode: 0o755 });
    }
    fs.writeFileSync(path.join(dir, "scripts/ci/mutation-gate.sh"), "#!/bin/bash\nexit 0\n", {
      mode: 0o755,
    });
    const name = advisory
      ? "Measure the default-disabled mutators"
      : "Run Gremlins mutation testing";
    const step = workflow.split(`      - name: ${name}\n`)[1];
    assert.ok(step);
    const lines = step.split("        run: |\n")[1].split("\n");
    const body = [];
    for (const line of lines) {
      if (line.trim() && !line.startsWith("          ")) break;
      body.push(line.slice(10));
    }
    const script = body.join("\n").replace(/\$\{\{ matrix\.([^}]+) \}\}/gu, (_, key) => {
      if (key === "package") return pkg;
      if (key === "zero_mutants || false") return "false";
      if (key === "efficacy" || key === "mcover") return "100";
      return "";
    });
    const result = spawnSync("bash", ["-c", script], {
      cwd: dir,
      encoding: "utf8",
      timeout: 10_000,
      env: {
        ...process.env,
        PATH: `${path.join(dir, "bin")}:${process.env.PATH}`,
        EVENTS: path.join(dir, "events"),
        GO_ARGS: path.join(dir, "go-args"),
        GREMLINS_ARGS: path.join(dir, "gremlins-args"),
        BASELINE_STATUS: baselineFails ? "1" : "0",
        PACKAGES: `${pkg}|fixture|100`,
      },
    });
    const read = (file) =>
      fs.existsSync(path.join(dir, file))
        ? fs.readFileSync(path.join(dir, file), "utf8").trim().split("\n")
        : [];
    return { ...result, events: read("events"), go: read("go-args"), args: read("gremlins-args") };
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

test("CLI mutation tests use the real module suite after a matching baseline", () => {
  const result = runGatingStep("./cmd/portwing");
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(result.events, ["go", "gremlins"]);
  assert.deepEqual(result.go, ["test", "-tags=", "./..."]);
  assert.ok(result.args.includes("--integration"));
  assert.equal(result.args.at(-1), "./cmd/portwing");
});

test("a broken full-suite baseline cannot produce false CLI kills", () => {
  const result = runGatingStep("./cmd/portwing", true);
  assert.notEqual(result.status, 0);
  assert.deepEqual(result.events, ["go"]);
});

test("banner excludes only its generator, while runtime packages keep their scope", () => {
  const banner = runGatingStep("./internal/banner");
  assert.equal(banner.status, 0, banner.stderr);
  const exclusion = banner.args[banner.args.indexOf("--exclude-files") + 1];
  assert.ok(exclusion, "banner generator exclusion missing");
  const regex = new RegExp(exclusion, "u");
  assert.match("internal/banner/gen/main.go", regex);
  assert.doesNotMatch("internal/banner/banner.go", regex);
  assert.doesNotMatch("internal/banner/tty_unix.go", regex);
  const server = runGatingStep("./internal/server");
  assert.equal(server.status, 0, server.stderr);
  assert.ok(!server.args.includes("--exclude-files"));
  assert.ok(!server.args.includes("--integration"));
});

test("branch validation cannot cancel another ref's monthly mutation run", () => {
  const group = workflow.match(/^ {2}group: (.+)$/mu)?.[1];
  assert.ok(group?.includes("github.ref"), "mutation concurrency must include the ref");
});

test("advisory CLI and banner measurements use the same corrected scope", () => {
  const cli = runGatingStep("./cmd/portwing", false, true);
  assert.equal(cli.status, 0, cli.stderr);
  assert.deepEqual(cli.events, ["go", "gremlins"]);
  assert.ok(cli.args.includes("--integration"));
  const broken = runGatingStep("./cmd/portwing", true, true);
  assert.notEqual(broken.status, 0);
  assert.deepEqual(broken.events, ["go"]);
  const banner = runGatingStep("./internal/banner", false, true);
  assert.equal(banner.status, 0, banner.stderr);
  assert.ok(banner.args.includes("--exclude-files"));
  const server = runGatingStep("./internal/server", false, true);
  assert.equal(server.status, 0, server.stderr);
  assert.ok(!server.args.includes("--integration"));
  assert.ok(!server.args.includes("--exclude-files"));
});
