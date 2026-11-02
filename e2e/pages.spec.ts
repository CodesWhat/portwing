// SPDX-License-Identifier: MIT

import { expect } from "@playwright/test";
import { MOBILE, overflowsHorizontally, test } from "./fixtures";

test.describe("compare at 375px", () => {
  test.use({ viewport: MOBILE });

  for (const route of ["/compare", "/compare/portainer"]) {
    test(`${route} has no horizontal overflow`, async ({ page }) => {
      const response = await page.goto(route);
      expect(response?.status()).toBe(200);
      await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
      expect(await overflowsHorizontally(page)).toBe(false);
    });
  }
});

test("a missing path serves the 404 page with a 404 status", async ({ page, problems }) => {
  const response = await page.goto("/no-such-page");
  // The browser logs the 404 document itself as a console error; that is the point here.
  const expected = (problem: string) => /status of 404.*\/no-such-page/.test(problem);
  problems.splice(0, problems.length, ...problems.filter((problem) => !expected(problem)));
  expect(response?.status()).toBe(404);
  expect(response?.headers()["content-security-policy"]).toContain("default-src 'self'");
  await expect(page.getByRole("link", { name: /home/i }).first()).toBeVisible();
});
