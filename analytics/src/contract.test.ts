import assert from "node:assert/strict";
import test from "node:test";

import {
  buildCtaEvent,
  buildPageviewEvent,
  buildWebVitalsEvent,
  canonicalizeAnalyticsRoute,
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
  assert.equal(buildCtaEvent("/", "docs_security" as never, "hero"), null);
  assert.equal(buildCtaEvent("/", "install_secure", "header"), null);
  assert.equal(buildCtaEvent("/", "free-form" as never, "hero"), null);
  assert.equal(buildCtaEvent("/", "docs_root", "free-form" as never), null);
});

test("before_send reconstructs a strict event and property allowlist", () => {
  assert.deepEqual(
    sanitizeEvent({
      event: "$pageview",
      uuid: "internal-posthog-id",
      properties: {
        ...BASE_PROPERTIES,
        $current_url: "https://portwing.codeswhat.com/docs?token=secret#private",
        token: "phc_project-token",
        $cookieless_mode: true,
        $process_person_profile: false,
        $referrer: "https://search.example/private",
        title: "customer private title",
      },
    }),
    {
      event: "$pageview",
      uuid: "internal-posthog-id",
      properties: {
        ...BASE_PROPERTIES,
        surface: "docs",
        path: "/docs",
        token: "phc_project-token",
        $cookieless_mode: true,
        $process_person_profile: false,
      },
    },
  );
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
      uuid: "internal-posthog-id",
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
      uuid: "internal-posthog-id",
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
      uuid: "internal-posthog-id",
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

type BufferedEvent = {
  event: "$web_vitals";
  properties: Record<string, unknown>;
};

type WebVitalsReporterFactory = (
  initialPath: string,
  emit: (event: BufferedEvent) => void,
  buildEvent: typeof buildWebVitalsEvent,
  canonicalizePath: typeof canonicalizeAnalyticsRoute,
  schedule: (callback: () => void, delayMs: number) => number,
  cancel: (timer: number) => void,
) => {
  record: (name: string, value: number) => void;
};

async function loadWebVitalsReporterFactory(): Promise<WebVitalsReporterFactory> {
  const module = await import("./web-vitals-buffer.ts").catch(() => ({}));
  const factory = "createWebVitalsReporter" in module ? module.createWebVitalsReporter : undefined;
  assert.equal(typeof factory, "function", "createWebVitalsReporter must exist");
  return factory as WebVitalsReporterFactory;
}

function fakeTimers() {
  let nextTimer = 1;
  const pending = new Map<number, () => void>();
  const delays: number[] = [];
  return {
    cancel(timer: number) {
      pending.delete(timer);
    },
    delays,
    fire() {
      const callbacks = [...pending.values()];
      pending.clear();
      for (const callback of callbacks) callback();
    },
    pending,
    schedule(callback: () => void, delayMs: number) {
      const timer = nextTimer++;
      delays.push(delayMs);
      pending.set(timer, callback);
      return timer;
    },
  };
}

test("web vitals reporter emits one complete canonical envelope", async () => {
  const createWebVitalsReporter = await loadWebVitalsReporterFactory();
  const timers = fakeTimers();
  const emitted: BufferedEvent[] = [];
  const reporter = createWebVitalsReporter(
    "/docs/security-model?customer=secret#private",
    (event) => emitted.push(event),
    buildWebVitalsEvent,
    canonicalizeAnalyticsRoute,
    timers.schedule,
    timers.cancel,
  );

  reporter.record("CLS", 0.01);
  reporter.record("FCP", 123.4);
  reporter.record("INP", 81.25);
  reporter.record("LCP", 456.7);

  assert.deepEqual(timers.delays, [5_000]);
  assert.equal(timers.pending.size, 0);
  assert.deepEqual(emitted, [
    {
      event: "$web_vitals",
      properties: {
        ...BASE_PROPERTIES,
        surface: "docs",
        path: "/docs/security-model",
        $web_vitals_CLS_value: 0.01,
        $web_vitals_FCP_value: 123.4,
        $web_vitals_INP_value: 81.25,
        $web_vitals_LCP_value: 456.7,
      },
    },
  ]);

  reporter.record("LCP", 999);
  timers.fire();
  assert.equal(emitted.length, 1);
});

test("web vitals reporter flushes one partial envelope and ignores late metrics", async () => {
  const createWebVitalsReporter = await loadWebVitalsReporterFactory();
  const timers = fakeTimers();
  const emitted: BufferedEvent[] = [];
  const reporter = createWebVitalsReporter(
    "/?customer=secret#private",
    (event) => emitted.push(event),
    buildWebVitalsEvent,
    canonicalizeAnalyticsRoute,
    timers.schedule,
    timers.cancel,
  );

  reporter.record("TTFB", 8);
  reporter.record("CLS", -1);
  reporter.record("FCP", Number.NaN);
  assert.equal(timers.pending.size, 0);

  reporter.record("CLS", 0.02);
  reporter.record("LCP", 321);
  assert.deepEqual(timers.delays, [5_000]);
  timers.fire();

  assert.deepEqual(emitted, [
    {
      event: "$web_vitals",
      properties: {
        ...BASE_PROPERTIES,
        surface: "marketing",
        path: "/",
        $web_vitals_CLS_value: 0.02,
        $web_vitals_LCP_value: 321,
      },
    },
  ]);
  reporter.record("FCP", 111);
  reporter.record("INP", 42);
  assert.equal(emitted.length, 1);
});

test("web vitals reporters stay page-load scoped across navigation and revisits", async () => {
  const createWebVitalsReporter = await loadWebVitalsReporterFactory();
  const timers = fakeTimers();
  const emitted: BufferedEvent[] = [];
  const firstLoad = createWebVitalsReporter(
    "/docs/security-model?customer=secret#private",
    (event) => emitted.push(event),
    buildWebVitalsEvent,
    canonicalizeAnalyticsRoute,
    timers.schedule,
    timers.cancel,
  );

  firstLoad.record("CLS", 0.03);
  assert.deepEqual(buildPageviewEvent("/compare"), {
    event: "$pageview",
    properties: {
      ...BASE_PROPERTIES,
      surface: "marketing",
      path: "/compare",
    },
  });
  assert.equal(emitted.length, 0);
  firstLoad.record("LCP", 600);
  timers.fire();

  assert.deepEqual(emitted, [
    {
      event: "$web_vitals",
      properties: {
        ...BASE_PROPERTIES,
        surface: "docs",
        path: "/docs/security-model",
        $web_vitals_CLS_value: 0.03,
        $web_vitals_LCP_value: 600,
      },
    },
  ]);

  firstLoad.record("FCP", 111);
  timers.fire();
  assert.equal(emitted.length, 1);

  const revisit = createWebVitalsReporter(
    "/docs/security-model/",
    (event) => emitted.push(event),
    buildWebVitalsEvent,
    canonicalizeAnalyticsRoute,
    timers.schedule,
    timers.cancel,
  );
  revisit.record("INP", 42);
  timers.fire();

  assert.deepEqual(emitted[1], {
    event: "$web_vitals",
    properties: {
      ...BASE_PROPERTIES,
      surface: "docs",
      path: "/docs/security-model",
      $web_vitals_INP_value: 42,
    },
  });
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
      capture_performance: false,
      before_send: undefined,
    },
  );
  assert.equal("disable_external_dependency_loading" in options, false);
  assert.equal(options.before_send, sanitizeEvent);
});
