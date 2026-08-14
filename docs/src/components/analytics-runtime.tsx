"use client";

import { capturePageview, captureWebVital, type WebVitalName } from "@codeswhat/public-analytics";
import { usePathname } from "next/navigation";
import { useReportWebVitals } from "next/web-vitals";
import { useEffect } from "react";

export function AnalyticsRuntime() {
  const pathname = usePathname();

  useEffect(() => {
    capturePageview(pathname);
  }, [pathname]);

  useReportWebVitals(({ name, value }) => {
    captureWebVital(pathname, name as WebVitalName, value);
  });

  return null;
}
