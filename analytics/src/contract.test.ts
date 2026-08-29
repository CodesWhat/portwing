import assert from "node:assert/strict";
import test from "node:test";

import {
  buildCtaEvent,
  buildPageleaveEvent,
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
      $pathname: "/",
    },
  });
  assert.deepEqual(buildPageviewEvent("/docs/security-model?token=secret#threats"), {
    event: "$pageview",
    properties: {
      ...BASE_PROPERTIES,
      surface: "docs",
      path: "/docs/security-model",
      $pathname: "/docs/security-model",
    },
  });
  assert.deepEqual(buildPageviewEvent("/private/customer/acme?token=secret"), {
    event: "$pageview",
    properties: {
      ...BASE_PROPERTIES,
      surface: "marketing",
      path: "/_other",
      $pathname: "/_other",
    },
  });
  assert.deepEqual(buildPageviewEvent("/docs/private/customer/acme"), {
    event: "$pageview",
    properties: {
      ...BASE_PROPERTIES,
      surface: "docs",
      path: "/_other",
      $pathname: "/_other",
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

// posthog-js attaches these to every envelope by default (PostHog/posthog-js
// packages/browser-common/src/utils/event-utils.ts, getEventProperties()).
// before_send must forward them: PostHog's cookieless server-hash ingestion
// step reads them straight off event.properties and drops the event with a
// cookieless_missing_user_agent / cookieless_missing_host ingestion warning
// if either is absent (PostHog/posthog nodejs/src/ingestion/common/cookieless/
// cookieless-manager.ts, getProperties() + doBatchInner(), @e87e55a).
const COOKIELESS_HASH_PROPERTIES = {
  $raw_user_agent: "Mozilla/5.0 (Test Runner)",
  $host: "portwing.codeswhat.com",
};

test("before_send reconstructs a strict event and property allowlist", () => {
  assert.deepEqual(
    sanitizeEvent({
      event: "$pageview",
      uuid: "internal-posthog-id",
      properties: {
        ...BASE_PROPERTIES,
        ...COOKIELESS_HASH_PROPERTIES,
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
        $pathname: "/docs",
        token: "phc_project-token",
        $cookieless_mode: true,
        $process_person_profile: false,
        ...COOKIELESS_HASH_PROPERTIES,
      },
    },
  );
  assert.deepEqual(
    sanitizeEvent({
      event: "cta activated",
      uuid: "internal-posthog-id",
      properties: {
        ...BASE_PROPERTIES,
        ...COOKIELESS_HASH_PROPERTIES,
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
        ...COOKIELESS_HASH_PROPERTIES,
      },
    },
  );
  assert.equal(
    sanitizeEvent({
      event: "unknown",
      uuid: "internal-posthog-id",
      properties: {
        ...BASE_PROPERTIES,
        ...COOKIELESS_HASH_PROPERTIES,
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
        ...COOKIELESS_HASH_PROPERTIES,
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
          ...COOKIELESS_HASH_PROPERTIES,
          surface: "marketing",
          path: "/",
          ...privacyProperties,
        },
      }),
      null,
    );
  }
});

test("before_send requires and forwards the cookieless server-hash fields", () => {
  const validProperties = {
    ...BASE_PROPERTIES,
    ...COOKIELESS_HASH_PROPERTIES,
    surface: "marketing",
    path: "/",
    token: "phc_project-token",
    $cookieless_mode: true,
    $process_person_profile: false,
  };

  const result = sanitizeEvent({
    event: "$pageview",
    uuid: "internal-posthog-id",
    properties: validProperties,
  });
  assert.ok(result);
  assert.equal(result.properties.$raw_user_agent, COOKIELESS_HASH_PROPERTIES.$raw_user_agent);
  assert.equal(result.properties.$host, COOKIELESS_HASH_PROPERTIES.$host);

  // Regression guard: if before_send ever goes back to rebuilding properties
  // from an allowlist that forgets these two keys, cookieless ingestion drops
  // every event again with zero warning-free indication beyond
  // cookieless_missing_user_agent / cookieless_missing_host.
  for (const missingKey of Object.keys(COOKIELESS_HASH_PROPERTIES)) {
    const withoutField = { ...validProperties };
    delete withoutField[missingKey as keyof typeof withoutField];
    assert.equal(
      sanitizeEvent({
        event: "$pageview",
        uuid: "internal-posthog-id",
        properties: withoutField,
      }),
      null,
      `sanitizeEvent must drop events missing ${missingKey}`,
    );
  }
});

test("before_send forwards UTM campaign labels and $referring_domain at hostname granularity", () => {
  const result = sanitizeEvent({
    event: "$pageview",
    uuid: "internal-posthog-id",
    properties: {
      ...BASE_PROPERTIES,
      ...COOKIELESS_HASH_PROPERTIES,
      surface: "marketing",
      path: "/",
      $current_url: "https://portwing.codeswhat.com/",
      token: "phc_project-token",
      $cookieless_mode: true,
      $process_person_profile: false,
      $referring_domain: "news.ycombinator.com",
      utm_source: "newsletter",
      utm_medium: "email",
      utm_campaign: "launch",
      utm_content: "footer-link",
      utm_term: "portwing",
    },
  });
  assert.deepEqual(result?.properties.$referring_domain, "news.ycombinator.com");
  assert.deepEqual(result?.properties.utm_source, "newsletter");
  assert.deepEqual(result?.properties.utm_medium, "email");
  assert.deepEqual(result?.properties.utm_campaign, "launch");
  assert.deepEqual(result?.properties.utm_content, "footer-link");
  assert.deepEqual(result?.properties.utm_term, "portwing");
});

test("before_send drops a non-hostname $referring_domain instead of trimming it", () => {
  // A full referrer can carry a path from a private page someone was reading
  // before they clicked; trimming it to a hostname-shaped prefix would still
  // require parsing an untrusted string. Fail closed instead: an unexpected
  // shape is dropped whole, never partially forwarded.
  for (const badReferringDomain of [
    "https://news.ycombinator.com/item?id=123",
    "news.ycombinator.com/item?id=123",
    "javascript:alert(1)",
    "user:pass@news.ycombinator.com",
    "",
  ]) {
    const result = sanitizeEvent({
      event: "$pageview",
      uuid: "internal-posthog-id",
      properties: {
        ...BASE_PROPERTIES,
        ...COOKIELESS_HASH_PROPERTIES,
        surface: "marketing",
        path: "/",
        token: "phc_project-token",
        $cookieless_mode: true,
        $process_person_profile: false,
        $referring_domain: badReferringDomain,
      },
    });
    assert.equal(
      "$referring_domain" in (result?.properties ?? {}),
      false,
      `sanitizeEvent must drop non-hostname $referring_domain ${JSON.stringify(badReferringDomain)}`,
    );
  }

  // "$direct" is posthog-js's own sentinel for "no referrer", not a hostname
  // shape, and is the only non-hostname value that survives.
  const direct = sanitizeEvent({
    event: "$pageview",
    uuid: "internal-posthog-id",
    properties: {
      ...BASE_PROPERTIES,
      ...COOKIELESS_HASH_PROPERTIES,
      surface: "marketing",
      path: "/",
      token: "phc_project-token",
      $cookieless_mode: true,
      $process_person_profile: false,
      $referring_domain: "$direct",
    },
  });
  assert.equal(direct?.properties.$referring_domain, "$direct");
});

test("before_send never copies $referrer and excludes per-click identifiers", () => {
  const result = sanitizeEvent({
    event: "$pageview",
    uuid: "internal-posthog-id",
    properties: {
      ...BASE_PROPERTIES,
      ...COOKIELESS_HASH_PROPERTIES,
      surface: "marketing",
      path: "/",
      token: "phc_project-token",
      $cookieless_mode: true,
      $process_person_profile: false,
      $referrer: "https://news.ycombinator.com/item?id=123&private=customer-acme",
      $referring_domain: "news.ycombinator.com",
      // These arrive automatically alongside campaign params
      // (save_campaign_params) and are per-click identifiers a platform can
      // rejoin to a person, not campaign labels we authored.
      gclid: "Cj0KCQjw-click-id",
      fbclid: "IwAR-click-id",
      msclkid: "abc123click",
      gad_source: "1",
      mc_cid: "abc123",
    },
  });
  assert.ok(result);
  assert.equal("$referrer" in result.properties, false);
  assert.equal("gclid" in result.properties, false);
  assert.equal("fbclid" in result.properties, false);
  assert.equal("msclkid" in result.properties, false);
  assert.equal("gad_source" in result.properties, false);
  assert.equal("mc_cid" in result.properties, false);
  assert.equal(result.properties.$referring_domain, "news.ycombinator.com");
});

test("before_send drops a URL-shaped UTM value rather than forwarding it", () => {
  // Testing for "://" alone would pass the first of these and forward the
  // rest, each of which still carries a path.
  const leaky = [
    "https://portwing.internal/customer/acme?token=secret",
    "//portwing.internal/customer/acme",
    "/account/reset/abc123",
    "portwing.internal/customer/acme?k=v",
    "campaign?utm_secret=x",
    "page#fragment",
    "customer\\acme\\reset",
    // Doubly encoded: posthog-js decodes once, so this reaches the sanitizer as
    // "%2Facme%2Freset" with no literal separator left to match.
    "customer%252Facme%252Freset",
    "customer%2Facme%2Freset",
    "a".repeat(201),
  ];

  for (const utmContent of leaky) {
    const result = sanitizeEvent({
      event: "$pageview",
      uuid: "internal-posthog-id",
      properties: {
        ...BASE_PROPERTIES,
        ...COOKIELESS_HASH_PROPERTIES,
        surface: "marketing",
        path: "/",
        token: "phc_project-token",
        $cookieless_mode: true,
        $process_person_profile: false,
        utm_source: "newsletter",
        utm_content: utmContent,
      },
    });
    assert.ok(result);
    assert.equal(result.properties.utm_source, "newsletter");
    assert.equal(
      "utm_content" in result.properties,
      false,
      `sanitizeEvent must drop URL-shaped utm_content ${JSON.stringify(utmContent.slice(0, 48))}`,
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
        ...COOKIELESS_HASH_PROPERTIES,
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
        ...COOKIELESS_HASH_PROPERTIES,
      },
    },
  );
  assert.equal(
    sanitizeEvent({
      event: "$web_vitals",
      uuid: "internal-posthog-id",
      properties: {
        ...COOKIELESS_HASH_PROPERTIES,
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
      $pathname: "/compare",
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
      capture_pageleave: true,
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
      save_campaign_params: true,
      save_referrer: true,
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

test("pageleave mirrors the pageview contract and canonicalizes the same way", () => {
  assert.deepEqual(buildPageleaveEvent("/docs/security-model?token=secret#threats"), {
    event: "$pageleave",
    properties: {
      ...BASE_PROPERTIES,
      surface: "docs",
      path: "/docs/security-model",
      $pathname: "/docs/security-model",
    },
  });

  // posthog-js emits $pageleave itself once capture_pageleave is true, so it
  // reaches before_send carrying PostHog's own properties rather than ours.
  // The sanitizer has to rebuild it from $current_url like any other envelope;
  // before this branch existed it returned null and every $pageleave was
  // dropped silently, which is why flipping the option alone fixes nothing.
  assert.deepEqual(
    sanitizeEvent({
      event: "$pageleave",
      uuid: "internal-posthog-id",
      properties: {
        ...COOKIELESS_HASH_PROPERTIES,
        $current_url: "https://portwing.codeswhat.com/compare?token=secret#private",
        token: "phc_project-token",
        $cookieless_mode: true,
        $process_person_profile: false,
        $referrer: "https://search.example/private",
        title: "customer private title",
      },
    }),
    {
      event: "$pageleave",
      uuid: "internal-posthog-id",
      properties: {
        ...BASE_PROPERTIES,
        surface: "marketing",
        path: "/compare",
        $pathname: "/compare",
        token: "phc_project-token",
        $cookieless_mode: true,
        $process_person_profile: false,
        ...COOKIELESS_HASH_PROPERTIES,
      },
    },
  );
});

test("$pathname never diverges from the allowlisted path", () => {
  // $pathname exists so PostHog's Web analytics Page / Entry page / Exit page
  // tables resolve at all; they read that property and nothing else. It must
  // stay bound to the canonicalized path: if it ever carries the raw pathname,
  // every unlisted route starts leaking into the analytics project, which is
  // exactly what the MARKETING_PATHS/DOCS_PATHS allowlist exists to prevent.
  for (const rawPath of [
    "/",
    "/compare/watchtower",
    "/docs/security-model",
    "/private/customer/acme",
    "/docs/private/customer/acme?token=secret#fragment",
  ]) {
    for (const build of [buildPageviewEvent, buildPageleaveEvent]) {
      const { properties } = build(rawPath);
      assert.equal(properties.$pathname, properties.path);
      assert.equal(
        String(properties.$pathname).includes("customer"),
        false,
        `unlisted route leaked into $pathname for ${rawPath}`,
      );
    }
  }
});
