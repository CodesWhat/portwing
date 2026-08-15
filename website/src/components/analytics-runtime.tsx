"use client";

import { capturePageview, captureWebVital } from "@codeswhat/public-analytics";
import { usePathname } from "next/navigation";
import { useReportWebVitals } from "next/web-vitals";
import { useCallback, useEffect, useRef } from "react";

export function AnalyticsRuntime() {
  const pathname = usePathname();
  const webVitalsPath = useRef(pathname).current;

  useEffect(() => {
    capturePageview(pathname);
  }, [pathname]);

  const reportWebVital = useCallback<Parameters<typeof useReportWebVitals>[0]>(
    ({ name, value }) => {
      captureWebVital(webVitalsPath, name, value);
    },
    [webVitalsPath],
  );
  useReportWebVitals(reportWebVital);

  return null;
}
