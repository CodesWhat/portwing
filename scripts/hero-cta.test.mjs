// SPDX-License-Identifier: MIT

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import test from "node:test";

import { buildCtaEvent } from "../analytics/src/contract.ts";
import { HERO_PRIMARY_CTA, HERO_SECONDARY_CTA } from "../website/src/lib/hero-ctas.ts";

const ROOT = path.resolve(import.meta.dirname, "..");
const read = (file) => fs.readFileSync(path.join(ROOT, file), "utf8");

test("hero primary CTA goes to getting started and fires cta activated", () => {
  assert.equal(HERO_PRIMARY_CTA.href, "/docs/getting-started");
  assert.deepEqual(buildCtaEvent("/", HERO_PRIMARY_CTA.ctaId, HERO_PRIMARY_CTA.placement), {
    event: "cta activated",
    properties: {
      schema_version: 1,
      site: "portwing",
      surface: "marketing",
      path: "/",
      cta_id: "docs_getting_started",
      placement: "hero",
    },
  });
});

test("hero secondary CTA links the docs and is a valid event", () => {
  assert.equal(HERO_SECONDARY_CTA.href, "/docs");
  const event = buildCtaEvent("/", HERO_SECONDARY_CTA.ctaId, HERO_SECONDARY_CTA.placement);
  assert.equal(event?.event, "cta activated");
  assert.equal(event?.properties.cta_id, "docs_root");
});

test("the hero renders the primary CTA as the one primary button", () => {
  const source = read("website/src/components/cta-buttons.tsx");
  assert.equal(source.match(/className="btn primary"/g)?.length, 1);
  const primary = source.slice(source.indexOf('className="btn primary"') - 40);
  assert.match(primary.split("</TrackedLink>")[0], /HERO_PRIMARY_CTA\.href/u);
  assert.match(read("website/src/app/page.tsx"), /<CtaButtons \/>/u);
});
