import posthog from "posthog-js/dist/module.slim";

import {
  type AnalyticsEvent,
  buildCtaEvent,
  buildPageviewEvent,
  buildWebVitalsEvent,
  type CtaId,
  type CtaPlacement,
  createPostHogOptions,
} from "./contract";
import { createWebVitalsBuffer } from "./web-vitals-buffer";

let initialized = false;

export function initializeAnalytics(
  projectToken: string | undefined,
  proxyHost: string | undefined,
  uiHost: string | undefined,
): void {
  if (initialized) return;
  const options = createPostHogOptions(projectToken, proxyHost, uiHost);
  if (!options || !projectToken) return;
  posthog.init(projectToken, options);
  initialized = true;
}

function capture(event: AnalyticsEvent | null): void {
  if (!initialized || !event) return;
  posthog.capture(event.event, event.properties);
}

const webVitalsBuffer = createWebVitalsBuffer(capture, buildWebVitalsEvent);

export function capturePageview(path: string): void {
  webVitalsBuffer.begin(path);
  capture(buildPageviewEvent(path));
}

export function captureWebVital(path: string, name: string, value: number): void {
  webVitalsBuffer.record(path, name, value);
}

export function captureCta(path: string, ctaId: CtaId, placement: CtaPlacement): void {
  capture(buildCtaEvent(path, ctaId, placement));
}

export type { CtaId, CtaPlacement } from "./contract";
