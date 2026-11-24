// SPDX-License-Identifier: MIT

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

const ROOT = path.resolve(import.meta.dirname, "..");
const workflow = fs.readFileSync(
  path.join(ROOT, ".github/workflows/security-zap-baseline.yml"),
  "utf8",
);
const rules = fs.readFileSync(path.join(ROOT, ".zap/rules.tsv"), "utf8");
const serve = fs.readFileSync(path.join(ROOT, "e2e/serve.mjs"), "utf8");

const port = /^ {10}target: http:\/\/localhost:(\d+)$/mu.exec(workflow)?.[1];

test("ZAP baseline scans the built export on localhost, not the challenged live site", () => {
  assert.ok(port, "the ZAP target must be http://localhost:<port>");
  assert.doesNotMatch(workflow, /target: https?:\/\/portwing\.codeswhat\.com/u);
  assert.doesNotMatch(workflow, /^ +portwing\.codeswhat\.com:443$/mu);
});

test("ZAP baseline builds the export and serves it with the production headers", () => {
  assert.match(workflow, /npm ci\n\s+npm run build/u);
  assert.match(workflow, /node e2e\/serve\.mjs/u);
  assert.match(workflow, new RegExp(`E2E_PORT: "${port}"`, "u"));
  assert.match(serve, /headersForHTML/u);
  assert.match(serve, /website\/scripts\/security-headers\.mjs/u);
  assert.match(serve, new RegExp(`E2E_PORT \\?\\? ${port}\\b`, "u"));
});

test("ZAP baseline keeps its pinned image, schedule, permissions and artifact", () => {
  assert.match(workflow, /docker_name: ghcr\.io\/zaproxy\/zaproxy@sha256:[0-9a-f]{64}/u);
  assert.match(workflow, /rules_file_name: \.zap\/rules\.tsv/u);
  assert.match(workflow, /cron: '15 7 \* \* 1'/u);
  assert.match(workflow, /^permissions:\n {2}contents: read$/mu);
  assert.match(workflow, /egress-policy: block/u);
  assert.match(workflow, /path: report_html\.html/u);
  assert.match(workflow, /if: always\(\)\n\s+uses: actions\/upload-artifact@/u);
});

test("ZAP rules scope 90022 to the localhost URL form", () => {
  const line = rules.split("\n").find((row) => row.startsWith("90022\t"));
  assert.ok(line, "missing the 90022 rule");
  const [, action, pattern] = line.split("\t");
  assert.equal(action, "OUTOFSCOPE");
  assert.equal(pattern, `http://localhost:${port}/docs/drydock-integration$`);
  assert.doesNotMatch(rules, /^90022\tOUTOFSCOPE\thttps:\/\/portwing/mu);
});
