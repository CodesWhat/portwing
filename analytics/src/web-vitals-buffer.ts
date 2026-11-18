// SPDX-License-Identifier: MIT

import type { AnalyticsEvent, WebVitalName } from "./contract";

export const WEB_VITALS_FLUSH_DELAY_MS = 5_000;

const WEB_VITAL_NAMES = new Set<WebVitalName>(["CLS", "FCP", "INP", "LCP"]);

type Schedule = (callback: () => void, delayMs: number) => unknown;
type Cancel = (timer: unknown) => void;
type BuildEvent = (
  path: string,
  metrics: Readonly<Partial<Record<WebVitalName, number>>>,
) => AnalyticsEvent | null;
type CanonicalizePath = (path: string) => {
  normalizedPath: string;
  path: string;
  surface: string;
};

type SubscribeHidden = (onHidden: () => void) => () => void;

const defaultSchedule: Schedule = (callback, delayMs) => setTimeout(callback, delayMs);
const defaultCancel: Cancel = (timer) => clearTimeout(timer as ReturnType<typeof setTimeout>);

const defaultSubscribeHidden: SubscribeHidden = (onHidden) => {
  if (typeof document === "undefined" || typeof window === "undefined") return () => {};
  const onVisibility = () => {
    if (document.visibilityState === "hidden") onHidden();
  };
  document.addEventListener("visibilitychange", onVisibility);
  window.addEventListener("pagehide", onHidden);
  return () => {
    document.removeEventListener("visibilitychange", onVisibility);
    window.removeEventListener("pagehide", onHidden);
  };
};

export function createWebVitalsReporter(
  initialPath: string,
  emit: (event: AnalyticsEvent) => void,
  buildEvent: BuildEvent,
  canonicalizePath: CanonicalizePath,
  schedule: Schedule = defaultSchedule,
  cancel: Cancel = defaultCancel,
  subscribeHidden: SubscribeHidden = defaultSubscribeHidden,
) {
  const path = canonicalizePath(initialPath).normalizedPath;
  let pending: Partial<Record<WebVitalName, number>> = {};
  let timer: unknown;

  const flush = () => {
    if (timer !== undefined) {
      cancel(timer);
      timer = undefined;
    }
    if (Object.keys(pending).length === 0) return;
    const batch = pending;
    pending = {};
    const event = buildEvent(path, batch);
    if (event) emit(event);
  };

  const unsubscribe = subscribeHidden(flush);

  return {
    record(name: string, value: number): void {
      if (!WEB_VITAL_NAMES.has(name as WebVitalName)) return;
      if (!Number.isFinite(value) || value < 0) return;

      pending[name as WebVitalName] = value;
      if (WEB_VITAL_NAMES.size === Object.keys(pending).length) {
        flush();
        return;
      }
      if (timer === undefined) {
        timer = schedule(flush, WEB_VITALS_FLUSH_DELAY_MS);
      }
    },
    dispose(): void {
      flush();
      unsubscribe();
    },
  };
}
