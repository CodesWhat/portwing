"use client";

import { capturePageview, captureWebVital, type WebVitalName } from "@codeswhat/public-analytics";
import { usePathname } from "next/navigation";
import { useReportWebVitals } from "next/web-vitals";
import { useCallback, useEffect, useRef } from "react";

export function AnalyticsRuntime() {
  const pathname = usePathname();
  const pathnameRef = useRef(pathname);
  pathnameRef.current = pathname;

  useEffect(() => {
    capturePageview(pathname);
  }, [pathname]);

  const reportWebVital = useCallback<Parameters<typeof useReportWebVitals>[0]>(
    ({ name, value }) => {
      captureWebVital(pathnameRef.current, name as WebVitalName, value);
    },
    [],
  );
  useReportWebVitals(reportWebVital);

  return null;
}
