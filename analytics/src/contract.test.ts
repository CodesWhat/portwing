import assert from "node:assert/strict";
import test from "node:test";

import {
  buildCtaEvent,
  buildPageviewEvent,
  buildWebVitalsEvent,
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

test("web vitals emit only finite Core Web Vitals", () => {
  assert.deepEqual(buildWebVitalsEvent("/compare/portainer", "INP", 81.25), {
    event: "$web_vitals",
    properties: {
      ...BASE_PROPERTIES,
      surface: "marketing",
      path: "/compare/portainer",
      metric_name: "INP",
      metric_value: 81.25,
    },
  });
  assert.equal(buildWebVitalsEvent("/", "TTFB" as never, 4), null);
  assert.equal(buildWebVitalsEvent("/", "LCP", Number.NaN), null);
  assert.equal(buildWebVitalsEvent("/", "CLS", Number.POSITIVE_INFINITY), null);
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
      capture_pageview: false,
      capture_pageleave: false,
      capture_dead_clicks: false,
      capture_exceptions: false,
      enable_heatmaps: false,
      disable_session_recording: true,
      disable_surveys: true,
      disable_external_dependency_loading: true,
      advanced_disable_flags: true,
      advanced_disable_feature_flags: true,
      advanced_disable_feature_flags_on_first_load: true,
      capture_performance: false,
      cookieless_mode: "always",
      person_profiles: "never",
      persistence: "memory",
      respect_dnt: true,
      save_campaign_params: false,
      save_referrer: false,
      before_send: undefined,
    },
  );
  assert.equal(options.before_send, sanitizeEvent);
});
