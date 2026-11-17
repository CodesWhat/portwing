// SPDX-License-Identifier: MIT

import posthog from "posthog-js/dist/module.slim";

import {
  type AnalyticsEvent,
  buildCtaEvent,
  buildPageviewEvent,
  buildWebVitalsEvent,
  type CtaId,
  type CtaPlacement,
  canonicalizeAnalyticsRoute,
  createPostHogOptions,
  isTrackingRefused,
  type PrivacySignals,
} from "./contract";
import { createWebVitalsReporter as createBufferedWebVitalsReporter } from "./web-vitals-buffer";

let initialized = false;

function readSignals(): PrivacySignals {
  const nav = typeof navigator === "undefined" ? undefined : navigator;
  const win = typeof window === "undefined" ? undefined : window;
  return {
    doNotTrack: nav?.doNotTrack,
    globalPrivacyControl: (nav as { globalPrivacyControl?: unknown } | undefined)
      ?.globalPrivacyControl,
    windowDoNotTrack: (win as { doNotTrack?: unknown } | undefined)?.doNotTrack,
    msDoNotTrack: (nav as { msDoNotTrack?: unknown } | undefined)?.msDoNotTrack,
  };
}

const trackingRefused = (): boolean => isTrackingRefused(readSignals());

export function initializeAnalytics(
  projectToken: string | undefined,
  proxyHost: string | undefined,
  uiHost: string | undefined,
): void {
  if (initialized) return;
  if (trackingRefused()) return;
  const options = createPostHogOptions(projectToken, proxyHost, uiHost, trackingRefused);
  if (!options || !projectToken) return;
  posthog.init(projectToken, options);
  initialized = true;
}

function capture(event: AnalyticsEvent | null): void {
  if (!initialized || !event || trackingRefused()) return;
  posthog.capture(event.event, event.properties);
}

export function capturePageview(path: string): void {
  capture(buildPageviewEvent(path));
}

export function createWebVitalsReporter(path: string): (name: string, value: number) => void {
  const reporter = createBufferedWebVitalsReporter(
    path,
    capture,
    buildWebVitalsEvent,
    canonicalizeAnalyticsRoute,
  );
  return reporter.record;
}

export function captureCta(path: string, ctaId: CtaId, placement: CtaPlacement): void {
  capture(buildCtaEvent(path, ctaId, placement));
}

export type { CtaId, CtaPlacement } from "./contract";
