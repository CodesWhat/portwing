import type { BeforeSendFn, CaptureResult } from "posthog-js";

export const POSTHOG_PROXY_HOST = "https://e.codeswhat.com";
export const POSTHOG_UI_HOST = "https://us.posthog.com";

const SCHEMA_VERSION = 1;
const SITE = "portwing";
const PROJECT_TOKEN_PATTERN = /^phc_[A-Za-z0-9_-]+$/u;
const COOKIELESS_DISTINCT_ID = "$posthog_cookieless";

const MARKETING_PATHS = new Set([
  "/",
  "/compare",
  "/compare/arcane",
  "/compare/diun",
  "/compare/hawser",
  "/compare/komodo",
  "/compare/portainer",
  "/compare/watchtower",
]);

const DOCS_PATHS = new Set([
  "/docs",
  "/docs/api-reference",
  "/docs/audit-logging",
  "/docs/authentication",
  "/docs/competitive-landscape",
  "/docs/configuration",
  "/docs/connection-modes",
  "/docs/drydock-integration",
  "/docs/getting-started",
  "/docs/installation",
  "/docs/mcp-server",
  "/docs/migrating-from-watchtower",
  "/docs/observability",
  "/docs/security-model",
  "/docs/stability-policy",
  "/docs/standalone-mode",
  "/docs/verification",
]);

export type Surface = "marketing" | "docs";
export type WebVitalName = "CLS" | "FCP" | "INP" | "LCP";
export type CtaId =
  | "install_quick"
  | "install_secure"
  | "install_native"
  | "docs_root"
  | "docs_getting_started"
  | "docs_installation"
  | "github_org"
  | "github_repository"
  | "community_discord";
export type CtaPlacement = "header" | "hero" | "comparison" | "get_started" | "footer";

type EventProperties = Record<string, unknown>;

export type AnalyticsEvent = {
  event: "$pageview" | "$web_vitals" | "cta activated";
  properties: EventProperties;
};

export type PostHogOptions = {
  api_host: typeof POSTHOG_PROXY_HOST;
  ui_host: typeof POSTHOG_UI_HOST;
  autocapture: false;
  rageclick: false;
  capture_pageview: false;
  capture_pageleave: false;
  capture_heatmaps: false;
  capture_dead_clicks: false;
  capture_exceptions: false;
  disable_session_recording: true;
  disable_surveys: true;
  disable_surveys_automatic_display: true;
  disable_product_tours: true;
  disable_web_experiments: true;
  advanced_disable_flags: true;
  cookieless_mode: "always";
  person_profiles: "never";
  persistence: "memory";
  disable_persistence: true;
  respect_dnt: true;
  save_campaign_params: false;
  save_referrer: false;
  disable_capture_url_hashes: true;
  disable_scroll_properties: true;
  mask_all_element_attributes: true;
  mask_all_text: true;
  capture_performance: false;
  before_send: BeforeSendFn;
};

const CTA_COMBINATIONS = new Set<string>([
  "install_quick:get_started",
  "install_secure:get_started",
  "install_native:get_started",
  "docs_root:header",
  "docs_root:hero",
  "docs_root:footer",
  "docs_getting_started:get_started",
  "docs_installation:get_started",
  "github_org:footer",
  "github_repository:header",
  "github_repository:hero",
  "github_repository:footer",
  "community_discord:footer",
]);

const WEB_VITAL_KEYS = [
  "$web_vitals_CLS_value",
  "$web_vitals_FCP_value",
  "$web_vitals_INP_value",
  "$web_vitals_LCP_value",
] as const;

const WEB_VITAL_NAMES: readonly WebVitalName[] = ["CLS", "FCP", "INP", "LCP"];

function normalizedPath(rawPath: string): string {
  const withoutQuery = rawPath.split(/[?#]/u, 1)[0] || "/";
  if (!withoutQuery.startsWith("/")) return "/";
  if (withoutQuery.length > 1 && withoutQuery.endsWith("/")) {
    return withoutQuery.slice(0, -1);
  }
  return withoutQuery;
}

export function canonicalizeAnalyticsRoute(rawPath: string): {
  normalizedPath: string;
  path: string;
  surface: Surface;
} {
  const normalized = normalizedPath(rawPath);
  const surface: Surface =
    normalized === "/docs" || normalized.startsWith("/docs/") ? "docs" : "marketing";
  const known = surface === "docs" ? DOCS_PATHS : MARKETING_PATHS;
  return {
    normalizedPath: normalized,
    path: known.has(normalized) ? normalized : "/_other",
    surface,
  };
}

function baseProperties(rawPath: string) {
  const { path, surface } = canonicalizeAnalyticsRoute(rawPath);
  return {
    schema_version: SCHEMA_VERSION,
    site: SITE,
    surface,
    path,
  } as const;
}

export function buildPageviewEvent(rawPath: string): AnalyticsEvent {
  return { event: "$pageview", properties: baseProperties(rawPath) };
}

export function buildCtaEvent(
  rawPath: string,
  ctaId: CtaId,
  placement: CtaPlacement,
): AnalyticsEvent | null {
  if (!CTA_COMBINATIONS.has(`${ctaId}:${placement}`)) return null;
  return {
    event: "cta activated",
    properties: {
      ...baseProperties(rawPath),
      cta_id: ctaId,
      placement,
    },
  };
}

export function buildWebVitalsEvent(
  rawPath: string,
  metrics: Readonly<Partial<Record<WebVitalName, number>>>,
): AnalyticsEvent | null {
  const properties: EventProperties = baseProperties(rawPath);
  let metricCount = 0;
  for (const name of WEB_VITAL_NAMES) {
    const value = metrics[name];
    if (typeof value === "number" && Number.isFinite(value) && value >= 0) {
      properties[`$web_vitals_${name}_value`] = value;
      metricCount += 1;
    }
  }
  return metricCount === 0 ? null : { event: "$web_vitals", properties };
}

function rawPathFromProperties(properties: EventProperties): string | undefined {
  if (typeof properties.path === "string") return properties.path;
  if (typeof properties.$current_url !== "string") return undefined;
  try {
    return new URL(properties.$current_url).pathname;
  } catch {
    return undefined;
  }
}

export const sanitizeEvent: BeforeSendFn = (envelope): CaptureResult | null => {
  if (!envelope || typeof envelope.event !== "string" || !envelope.properties) return null;
  const rawPath = rawPathFromProperties(envelope.properties);
  const token = envelope.properties.token;
  // PostHog's cookieless server-hash ingestion step computes the anonymous
  // distinct id from day + team + $ip + $host + $raw_user_agent. It reads
  // $raw_user_agent/$host straight off event.properties (not headers) and
  // silently drops the event with a cookieless_missing_user_agent /
  // cookieless_missing_host ingestion warning if either is absent
  // (PostHog/posthog nodejs/src/ingestion/common/cookieless/cookieless-manager.ts,
  // getProperties()/doBatchInner(), commit e87e55a). posthog-js attaches both
  // to every envelope by default (PostHog/posthog-js
  // packages/browser-common/src/utils/event-utils.ts, getEventProperties()),
  // so they must survive the allowlist rebuild below. $ip is deliberately
  // NOT forwarded here: posthog-js never sends it, and PostHog's capture
  // service fills it in from the request's own connection IP when absent
  // (nodejs/src/common/utils/event.ts, sanitizeEvent()) — a client-supplied
  // $ip would only be able to make that worse, never better.
  const rawUserAgent = envelope.properties.$raw_user_agent;
  const host = envelope.properties.$host;
  if (
    typeof rawPath !== "string" ||
    typeof token !== "string" ||
    !PROJECT_TOKEN_PATTERN.test(token) ||
    envelope.properties.$cookieless_mode !== true ||
    envelope.properties.$process_person_profile !== false ||
    typeof rawUserAgent !== "string" ||
    rawUserAgent === "" ||
    typeof host !== "string" ||
    host === ""
  ) {
    return null;
  }

  let sanitized: AnalyticsEvent | null;
  if (envelope.event === "$pageview") {
    sanitized = buildPageviewEvent(rawPath);
  } else if (envelope.event === "cta activated") {
    const ctaId = envelope.properties.cta_id;
    const placement = envelope.properties.placement;
    if (typeof ctaId !== "string" || typeof placement !== "string") return null;
    sanitized = buildCtaEvent(rawPath, ctaId as CtaId, placement as CtaPlacement);
  } else if (envelope.event === "$web_vitals") {
    const metrics: Partial<Record<WebVitalName, number>> = {};
    for (const [index, key] of WEB_VITAL_KEYS.entries()) {
      const value = envelope.properties[key];
      if (typeof value === "number") {
        metrics[WEB_VITAL_NAMES[index]] = value;
      }
    }
    sanitized = buildWebVitalsEvent(rawPath, metrics);
  } else {
    return null;
  }

  if (!sanitized) return null;
  const output: CaptureResult = {
    event: sanitized.event,
    properties: {
      ...sanitized.properties,
      token,
      $cookieless_mode: true,
      $process_person_profile: false,
      $raw_user_agent: rawUserAgent,
      $host: host,
    },
    uuid: envelope.uuid,
  };
  if (envelope.properties.distinct_id === COOKIELESS_DISTINCT_ID) {
    output.properties.distinct_id = COOKIELESS_DISTINCT_ID;
  }
  if (envelope.timestamp instanceof Date && Number.isFinite(envelope.timestamp.getTime())) {
    output.timestamp = envelope.timestamp;
  }
  return output;
};

export function createPostHogOptions(
  projectToken: string | undefined,
  proxyHost: string | undefined,
  uiHost: string | undefined,
): PostHogOptions | null {
  if (
    !projectToken ||
    !PROJECT_TOKEN_PATTERN.test(projectToken) ||
    proxyHost !== POSTHOG_PROXY_HOST ||
    uiHost !== POSTHOG_UI_HOST
  ) {
    return null;
  }
  return {
    api_host: POSTHOG_PROXY_HOST,
    ui_host: POSTHOG_UI_HOST,
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
    before_send: sanitizeEvent,
  };
}
