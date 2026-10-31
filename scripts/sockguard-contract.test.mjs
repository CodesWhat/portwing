// SPDX-License-Identifier: MIT
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import test from "node:test";

const root = new URL("../", import.meta.url);
const read = (path) => readFileSync(new URL(path, root), "utf8");
const provenance = JSON.parse(read("examples/sockguard-provenance.json"));

for (const [file, source] of Object.entries(provenance.files)) {
  test(`${file} matches the released Sockguard preset`, () => {
    const copy = read(`examples/${file}`);
    assert.equal(
      copy.split("\n", 1)[0],
      `# Copy of sockguard/${source.source} at ${provenance.tag}.`,
    );
    const body = copy.slice(copy.indexOf("\n") + 1);
    assert.equal(createHash("sha256").update(body).digest("hex"), source.sha256);
  });
}

for (const file of [
  "README.md",
  "examples/docker-compose.with-sockguard.yml",
  "examples/docker-compose.edge-with-exec.yml",
  "docs/content/docs/current/security-model.mdx",
  "docs/content/docs/current/getting-started.mdx",
]) {
  test(`${file} pins the reviewed Sockguard release`, () => {
    const images = read(file).match(/ghcr\.io\/codeswhat\/sockguard:[^\s`]+/g);
    assert.ok(images?.length, "expected a Sockguard deployment image");
    assert.deepEqual(
      [...new Set(images)],
      [`ghcr.io/codeswhat/sockguard:${provenance.tag.slice(1)}`],
    );
  });
}

test("the exec bundle protects custom proxy image overrides", () => {
  const compose = read("examples/docker-compose.edge-with-exec.yml");
  const proxy = compose.split("  sockguard:\n")[1]?.split("\n  portwing:")[0];
  assert.ok(proxy, "expected the sockguard service");
  assert.match(proxy, /\n {4}labels:\n {6}com\.sockguard\.exec: "deny"\n/);
});
