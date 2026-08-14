import posthog from "posthog-js/dist/module.slim";

import {
  buildCtaEvent,
  buildPageviewEvent,
  type CtaId,
  type CtaPlacement,
  createPostHogOptions,
} from "./contract";

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

function capture(event: ReturnType<typeof buildPageviewEvent> | null): void {
  if (!initialized || !event) return;
  posthog.capture(event.event, event.properties);
}

export function capturePageview(path: string): void {
  capture(buildPageviewEvent(path));
}

export function captureCta(path: string, ctaId: CtaId, placement: CtaPlacement): void {
  capture(buildCtaEvent(path, ctaId, placement));
}

export type { CtaId, CtaPlacement } from "./contract";
