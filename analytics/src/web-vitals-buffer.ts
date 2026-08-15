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

const defaultSchedule: Schedule = (callback, delayMs) => setTimeout(callback, delayMs);
const defaultCancel: Cancel = (timer) => clearTimeout(timer as ReturnType<typeof setTimeout>);

export function createWebVitalsBuffer(
  emit: (event: AnalyticsEvent) => void,
  buildEvent: BuildEvent,
  canonicalizePath: CanonicalizePath,
  schedule: Schedule = defaultSchedule,
  cancel: Cancel = defaultCancel,
) {
  let path: string | null = null;
  let routeKey: string | null = null;
  let metrics: Partial<Record<WebVitalName, number>> = {};
  let timer: unknown;
  let sent = false;
  const emittedPaths = new Set<string>();

  const flush = () => {
    if (sent || path === null) return;
    sent = true;
    if (timer !== undefined) {
      cancel(timer);
      timer = undefined;
    }
    const event = buildEvent(path, metrics);
    if (event && routeKey !== null) {
      emittedPaths.add(routeKey);
      emit(event);
    }
  };

  const reset = (nextPath: string) => {
    const route = canonicalizePath(nextPath);
    const nextRouteKey = `${route.surface}:${route.path}`;
    if (path !== null && !sent) flush();
    path = route.normalizedPath;
    routeKey = nextRouteKey;
    metrics = {};
    timer = undefined;
    sent = emittedPaths.has(nextRouteKey);
  };

  return {
    begin(nextPath: string): void {
      reset(nextPath);
    },
    record(nextPath: string, name: string, value: number): void {
      const route = canonicalizePath(nextPath);
      const nextRouteKey = `${route.surface}:${route.path}`;
      if (emittedPaths.has(nextRouteKey)) return;
      if (path === null || routeKey !== nextRouteKey) reset(nextPath);
      if (sent || !WEB_VITAL_NAMES.has(name as WebVitalName)) return;
      if (!Number.isFinite(value) || value < 0) return;

      metrics[name as WebVitalName] = value;
      if (timer === undefined) {
        timer = schedule(flush, WEB_VITALS_FLUSH_DELAY_MS);
      }
      if (WEB_VITAL_NAMES.size === Object.keys(metrics).length) flush();
    },
  };
}
