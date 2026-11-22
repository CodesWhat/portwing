// SPDX-License-Identifier: MIT

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

type PostHogClient = typeof import("posthog-js/dist/module.slim")["default"];
type QueuedCapture = { event: AnalyticsEvent; timestamp: Date };

// posthog-js is ~158 KB of script. Nothing about the first paint needs it, so it
// loads after the page's own load event plus an idle slot, and events raised
// before then wait in a bounded queue with their original timestamps.
const MAX_QUEUED_CAPTURES = 50;
const LOAD_FALLBACK_MS = 4_000;
const IDLE_TIMEOUT_MS = 2_000;

let initialized = false;
let started = false;
let posthog: PostHogClient | null = null;
let queued: QueuedCapture[] = [];

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

function whenIdleAfterLoad(task: () => void): void {
  let ran = false;
  const run = () => {
    if (ran) return;
    ran = true;
    const idle = (window as { requestIdleCallback?: typeof requestIdleCallback })
      .requestIdleCallback;
    if (idle) idle(task, { timeout: IDLE_TIMEOUT_MS });
    else setTimeout(task, 0);
  };
  if (document.readyState === "complete") run();
  else window.addEventListener("load", run, { once: true });
  setTimeout(run, LOAD_FALLBACK_MS);
}

function sendCapture({ event, timestamp }: QueuedCapture): void {
  posthog?.capture(event.event, event.properties, { timestamp });
}

export function initializeAnalytics(
  projectToken: string | undefined,
  proxyHost: string | undefined,
  uiHost: string | undefined,
): void {
  if (started) return;
  if (trackingRefused()) return;
  const options = createPostHogOptions(projectToken, proxyHost, uiHost, trackingRefused);
  if (!options || !projectToken) return;
  started = true;
  whenIdleAfterLoad(() => {
    import("posthog-js/dist/module.slim")
      .then((module) => {
        if (trackingRefused()) {
          queued = [];
          return;
        }
        posthog = module.default;
        posthog.init(projectToken, options);
        initialized = true;
        const pending = queued;
        queued = [];
        for (const capture of pending) sendCapture(capture);
      })
      .catch(() => {
        queued = [];
      });
  });
}

function capture(event: AnalyticsEvent | null): void {
  if (!started || !event || trackingRefused()) return;
  const entry = { event, timestamp: new Date() };
  if (initialized) {
    sendCapture(entry);
  } else if (queued.length < MAX_QUEUED_CAPTURES) {
    queued.push(entry);
  }
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
export { docsAnalyticsPath } from "./contract";
