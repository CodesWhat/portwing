import assert from "node:assert/strict";
import test from "node:test";

import {
  buildCtaEvent,
  buildPageviewEvent,
  createPostHogOptions,
  POSTHOG_PROXY_HOST,
  POSTHOG_UI_HOST,
  sanitizeEvent,
} from "./contract.ts";

const BASE_PROPERTIES = {
  schema_version: 1,
  site: "portwing",
};

test("pageviews canonicalize known routes and collapse unknown routes", () => {
  assert.deepEqual(buildPageviewEvent("/?utm_source=test#hero"), {
    event: "$pageview",
    properties: {
      ...BASE_PROPERTIES,
      surface: "marketing",
      path: "/",
    },
  });
  assert.deepEqual(buildPageviewEvent("/docs/security-model?token=secret#threats"), {
    event: "$pageview",
    properties: {
      ...BASE_PROPERTIES,
      surface: "docs",
      path: "/docs/security-model",
    },
  });
  assert.deepEqual(buildPageviewEvent("/private/customer/acme?token=secret"), {
    event: "$pageview",
    properties: {
      ...BASE_PROPERTIES,
      surface: "marketing",
      path: "/_other",
    },
  });
  assert.deepEqual(buildPageviewEvent("/docs/private/customer/acme"), {
    event: "$pageview",
    properties: {
      ...BASE_PROPERTIES,
      surface: "docs",
      path: "/_other",
    },
  });
});

test("CTA events accept only real Portwing component combinations", () => {
  assert.deepEqual(buildCtaEvent("/", "install_secure", "get_started"), {
    event: "cta activated",
    properties: {
      ...BASE_PROPERTIES,
      surface: "marketing",
      path: "/",
      cta_id: "install_secure",
      placement: "get_started",
    },
  });
  assert.deepEqual(buildCtaEvent("/docs", "github_repository", "header"), {
    event: "cta activated",
    properties: {
      ...BASE_PROPERTIES,
      surface: "docs",
      path: "/docs",
      cta_id: "github_repository",
      placement: "header",
    },
  });
  assert.equal(buildCtaEvent("/", "docs_security", "hero"), null);
  assert.equal(buildCtaEvent("/", "install_secure", "header"), null);
  assert.equal(buildCtaEvent("/", "free-form" as never, "hero"), null);
  assert.equal(buildCtaEvent("/", "docs_root", "free-form" as never), null);
});

test("before_send reconstructs a strict event and property allowlist", () => {
  assert.deepEqual(
    sanitizeEvent({
      event: "cta activated",
      uuid: "internal-posthog-id",
      properties: {
        ...BASE_PROPERTIES,
        surface: "marketing",
        path: "/?token=secret",
        token: "phc_project-token",
        $cookieless_mode: true,
        $process_person_profile: false,
        cta_id: "docs_root",
        placement: "hero",
        $current_url: "https://portwing.codeswhat.com/?token=secret",
        $referrer: "https://search.example/private",
        title: "customer private title",
        distinct_id: "person-id",
      },
    }),
    {
      event: "cta activated",
      uuid: "internal-posthog-id",
      properties: {
        ...BASE_PROPERTIES,
        surface: "marketing",
        path: "/",
        token: "phc_project-token",
        $cookieless_mode: true,
        $process_person_profile: false,
        cta_id: "docs_root",
        placement: "hero",
      },
    },
  );
  assert.equal(
    sanitizeEvent({
      event: "unknown",
      properties: {
        ...BASE_PROPERTIES,
        token: "phc_project-token",
        $cookieless_mode: true,
        $process_person_profile: false,
      },
    }),
    null,
  );
  assert.equal(
    sanitizeEvent({
      event: "cta activated",
      properties: {
        ...BASE_PROPERTIES,
        surface: "marketing",
        path: "/",
        token: "phc_project-token",
        $cookieless_mode: true,
        $process_person_profile: false,
        cta_id: "install_native",
        placement: "header",
      },
    }),
    null,
  );
  for (const privacyProperties of [
    { token: "phc_project-token", $cookieless_mode: false, $process_person_profile: false },
    { token: "phc_project-token", $cookieless_mode: true, $process_person_profile: true },
    { token: undefined, $cookieless_mode: true, $process_person_profile: false },
  ]) {
    assert.equal(
      sanitizeEvent({
        event: "$pageview",
        uuid: "internal-posthog-id",
        properties: {
          ...BASE_PROPERTIES,
          surface: "marketing",
          path: "/",
          ...privacyProperties,
        },
      }),
      null,
    );
  }
});

test("before_send keeps one buffered Core Web Vitals envelope", () => {
  const timestamp = new Date("2026-08-14T00:00:00Z");
  assert.deepEqual(
    sanitizeEvent({
      event: "$web_vitals",
      uuid: "internal-posthog-id",
      timestamp,
      properties: {
        $current_url: "https://portwing.codeswhat.com/docs?secret=1#private",
        token: "phc_project-token",
        distinct_id: "$posthog_cookieless",
        $cookieless_mode: true,
        $process_person_profile: false,
        $web_vitals_CLS_value: 0.01,
        $web_vitals_FCP_value: 123.4,
        $web_vitals_INP_value: -1,
        $web_vitals_LCP_value: 456.7,
        $web_vitals_TTFB_value: 8,
        $web_vitals_LCP_event: { navigationEntry: "private" },
        metric_name: "INP",
        metric_value: 81.25,
      },
    }),
    {
      event: "$web_vitals",
      uuid: "internal-posthog-id",
      timestamp,
      properties: {
        ...BASE_PROPERTIES,
        surface: "docs",
        path: "/docs",
        token: "phc_project-token",
        distinct_id: "$posthog_cookieless",
        $cookieless_mode: true,
        $process_person_profile: false,
        $web_vitals_CLS_value: 0.01,
        $web_vitals_FCP_value: 123.4,
        $web_vitals_LCP_value: 456.7,
      },
    },
  );
  assert.equal(
    sanitizeEvent({
      event: "$web_vitals",
      properties: {
        path: "/",
        token: "phc_project-token",
        $cookieless_mode: true,
        $process_person_profile: false,
        $web_vitals_INP_value: Number.NaN,
        $web_vitals_LCP_value: -1,
        metric_name: "LCP",
        metric_value: 123,
      },
    }),
    null,
  );
});

test("PostHog initializes only with the exact production proxy contract", () => {
  assert.equal(createPostHogOptions(undefined, undefined, undefined), null);
  assert.equal(createPostHogOptions("phc_project", POSTHOG_PROXY_HOST, undefined), null);
  assert.equal(
    createPostHogOptions("phc_project", "https://us.i.posthog.com", POSTHOG_UI_HOST),
    null,
  );
  assert.equal(
    createPostHogOptions("not-a-project-token", POSTHOG_PROXY_HOST, POSTHOG_UI_HOST),
    null,
  );

  const options = createPostHogOptions("phc_project-token", POSTHOG_PROXY_HOST, POSTHOG_UI_HOST);
  assert.ok(options);
  assert.deepEqual(
    { ...options, before_send: undefined },
    {
      api_host: "https://e.codeswhat.com",
      ui_host: "https://us.posthog.com",
      autocapture: false,
      rageclick: false,
      capture_pageview: false,
      capture_pageleave: false,
      capture_heatmaps: false,
      capture_dead_clicks: false,
      capture_exceptions: false,
      disable_session_recording: true,
      disable_surveys: true,
      disable_surveys_automatic_display: true,
      disable_product_tours: true,
      disable_web_experiments: true,
      advanced_disable_flags: true,
      cookieless_mode: "always",
      person_profiles: "never",
      persistence: "memory",
      disable_persistence: true,
      respect_dnt: true,
      save_campaign_params: false,
      save_referrer: false,
      disable_capture_url_hashes: true,
      disable_scroll_properties: true,
      mask_all_element_attributes: true,
      mask_all_text: true,
      capture_performance: {
        network_timing: false,
        web_vitals: true,
        web_vitals_allowed_metrics: ["CLS", "FCP", "INP", "LCP"],
        web_vitals_attribution: false,
      },
      before_send: undefined,
    },
  );
  assert.equal("disable_external_dependency_loading" in options, false);
  assert.equal(options.before_send, sanitizeEvent);
});
