import type { BeforeSendFn, CaptureResult } from "posthog-js";

export const POSTHOG_PROXY_HOST = "https://e.codeswhat.com";
export const POSTHOG_UI_HOST = "https://us.posthog.com";

const SCHEMA_VERSION = 1;
const SITE = "portwing";

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
export type CtaId =
  | "install_quick"
  | "install_secure"
  | "install_native"
  | "docs_root"
  | "docs_security"
  | "docs_getting_started"
  | "docs_installation"
  | "github_org"
  | "github_repository"
  | "community_discord";
export type CtaPlacement =
  | "header"
  | "hero"
  | "comparison"
  | "get_started"
  | "footer"
  | "star_history";
export type WebVitalName = "CLS" | "FCP" | "INP" | "LCP";

type EventProperties = Record<string, boolean | number | string>;

export type AnalyticsEvent = {
  event: "$pageview" | "$web_vitals" | "cta activated";
  properties: EventProperties;
};

export type PostHogOptions = {
  api_host: typeof POSTHOG_PROXY_HOST;
  ui_host: typeof POSTHOG_UI_HOST;
  autocapture: false;
  capture_pageview: false;
  capture_pageleave: false;
  capture_dead_clicks: false;
  capture_exceptions: false;
  enable_heatmaps: false;
  disable_session_recording: true;
  disable_surveys: true;
  disable_external_dependency_loading: true;
  advanced_disable_flags: true;
  advanced_disable_feature_flags: true;
  advanced_disable_feature_flags_on_first_load: true;
  capture_performance: false;
  cookieless_mode: "always";
  person_profiles: "never";
  persistence: "memory";
  respect_dnt: true;
  save_campaign_params: false;
  save_referrer: false;
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
  "github_repository:star_history",
  "community_discord:footer",
]);

const WEB_VITALS = new Set<WebVitalName>(["CLS", "FCP", "INP", "LCP"]);

function normalizedPath(rawPath: string): string {
  const withoutQuery = rawPath.split(/[?#]/u, 1)[0] || "/";
  if (!withoutQuery.startsWith("/")) return "/";
  if (withoutQuery.length > 1 && withoutQuery.endsWith("/")) {
    return withoutQuery.slice(0, -1);
  }
  return withoutQuery;
}

function canonicalRoute(rawPath: string): { path: string; surface: Surface } {
  const normalized = normalizedPath(rawPath);
  const surface: Surface =
    normalized === "/docs" || normalized.startsWith("/docs/") ? "docs" : "marketing";
  const known = surface === "docs" ? DOCS_PATHS : MARKETING_PATHS;
  return { path: known.has(normalized) ? normalized : "/_other", surface };
}

function baseProperties(rawPath: string) {
  const { path, surface } = canonicalRoute(rawPath);
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
  metricName: WebVitalName,
  metricValue: number,
): AnalyticsEvent | null {
  if (!WEB_VITALS.has(metricName) || !Number.isFinite(metricValue)) return null;
  return {
    event: "$web_vitals",
    properties: {
      ...baseProperties(rawPath),
      metric_name: metricName,
      metric_value: metricValue,
    },
  };
}

export const sanitizeEvent: BeforeSendFn = (envelope): CaptureResult | null => {
  if (!envelope || typeof envelope.event !== "string" || !envelope.properties) return null;
  const rawPath = envelope.properties.path;
  const token = envelope.properties.token;
  if (
    typeof rawPath !== "string" ||
    typeof token !== "string" ||
    !token.startsWith("phc_") ||
    envelope.properties.$cookieless_mode !== true ||
    envelope.properties.$process_person_profile !== false
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
    const metricName = envelope.properties.metric_name;
    const metricValue = envelope.properties.metric_value;
    if (typeof metricName !== "string" || typeof metricValue !== "number") return null;
    sanitized = buildWebVitalsEvent(rawPath, metricName as WebVitalName, metricValue);
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
    },
    uuid: envelope.uuid,
  };
  if (envelope.timestamp instanceof Date) output.timestamp = envelope.timestamp;
  return output;
};

export function createPostHogOptions(
  projectToken: string | undefined,
  proxyHost: string | undefined,
  uiHost: string | undefined,
): PostHogOptions | null {
  if (
    !projectToken?.startsWith("phc_") ||
    proxyHost !== POSTHOG_PROXY_HOST ||
    uiHost !== POSTHOG_UI_HOST
  ) {
    return null;
  }
  return {
    api_host: POSTHOG_PROXY_HOST,
    ui_host: POSTHOG_UI_HOST,
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
    before_send: sanitizeEvent,
  };
}
