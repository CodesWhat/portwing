// SPDX-License-Identifier: MIT

import { test as base, expect } from "@playwright/test";

export const MOBILE = { width: 375, height: 812 };

// 1x1 transparent SVG. External badges and the Star History chart are third parties, so a test
// must never depend on them being up (or on the network at all).
const STUB_IMAGE = '<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"></svg>';

type Problems = string[];

/**
 * Every test fails on a console error, an uncaught page error, a `securitypolicyviolation`
 * event, a request that left the local server for anything but a stubbed image, or a failed
 * local request, checked after the test body so no spec has to remember to. Browsers log CSP blocks as console errors too, but the event carries the
 * directive and blocked URL, so it is collected separately for a readable failure.
 */
export const test = base.extend<{ problems: Problems }>({
  problems: [
    async ({ page, context, baseURL }, use) => {
      const problems: Problems = [];
      const origin = new URL(baseURL ?? "http://127.0.0.1").origin;

      await context.route(
        (url) => url.origin !== origin,
        (route) => {
          const request = route.request();
          if (request.resourceType() === "image") {
            return route.fulfill({
              status: 200,
              contentType: "image/svg+xml",
              body: STUB_IMAGE,
            });
          }
          problems.push(`unexpected external ${request.resourceType()} request: ${request.url()}`);
          return route.abort();
        },
      );

      await page.exposeFunction("__reportViolation", (detail: string) => {
        problems.push(`securitypolicyviolation: ${detail}`);
      });
      await page.addInitScript(() => {
        document.addEventListener("securitypolicyviolation", (event) => {
          const report = (window as unknown as { __reportViolation: (detail: string) => void })
            .__reportViolation;
          report(`${event.effectiveDirective} blocked ${event.blockedURI || "inline"}`);
        });
      });
      page.on("console", (message) => {
        if (message.type() === "error") {
          problems.push(`console error: ${message.text()} (${message.location().url})`);
        }
      });
      page.on("pageerror", (error) => problems.push(`page error: ${error.message}`));
      page.on("requestfailed", (request) => {
        // Navigating away cancels in-flight prefetches; that is not a failure.
        if (
          request.url().startsWith(origin) &&
          !/abort|cancel/i.test(request.failure()?.errorText ?? "")
        ) {
          problems.push(`request failed: ${request.url()} ${request.failure()?.errorText}`);
        }
      });

      await use(problems);
      expect(problems, problems.join("\n")).toEqual([]);
    },
    { auto: true },
  ],
});

/** True when the page scrolls sideways, which at 375px means something overflowed the layout. */
export async function overflowsHorizontally(page: import("@playwright/test").Page) {
  return page.evaluate(
    () =>
      Math.max(document.documentElement.scrollWidth, document.body.scrollWidth) >
      document.documentElement.clientWidth,
  );
}
