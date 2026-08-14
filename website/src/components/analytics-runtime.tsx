"use client";

import { capturePageview } from "@codeswhat/public-analytics";
import { usePathname } from "next/navigation";
import { useEffect } from "react";

export function AnalyticsRuntime() {
  const pathname = usePathname();

  useEffect(() => {
    capturePageview(pathname);
  }, [pathname]);

  return null;
}
