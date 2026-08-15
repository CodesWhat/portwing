"use client";

import { capturePageview, createWebVitalsReporter } from "@codeswhat/public-analytics";
import { usePathname } from "next/navigation";
import { useReportWebVitals } from "next/web-vitals";
import { useCallback, useEffect, useRef } from "react";

export function AnalyticsRuntime() {
  const pathname = usePathname();
  const webVitalsReporter = useRef<ReturnType<typeof createWebVitalsReporter> | null>(null);
  if (webVitalsReporter.current === null) {
    webVitalsReporter.current = createWebVitalsReporter(pathname);
  }

  useEffect(() => {
    capturePageview(pathname);
  }, [pathname]);

  const reportWebVital = useCallback<Parameters<typeof useReportWebVitals>[0]>(
    ({ name, value }) => {
      webVitalsReporter.current?.(name, value);
    },
    [],
  );
  useReportWebVitals(reportWebVital);

  return null;
}
