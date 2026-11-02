// SPDX-License-Identifier: MIT

import { expect } from "@playwright/test";
import { MOBILE, overflowsHorizontally, test } from "./fixtures";

test.describe("docs at 375px", () => {
  test.use({ viewport: MOBILE });

  test("mobile drawer opens, closes on Escape and returns focus to its trigger", async ({
    page,
  }) => {
    await page.goto("/docs");
    const trigger = page.locator("#nd-subnav").getByRole("button", { name: "Open Sidebar" });
    const drawer = page.locator("#nd-sidebar-mobile");

    await trigger.click();
    await expect(drawer).toBeVisible();
    await expect(drawer.getByRole("button", { name: "Close Sidebar" })).toBeFocused();

    await page.keyboard.press("Escape");
    await expect(drawer).toBeHidden();
    await expect(trigger).toBeFocused();
  });

  test("api-reference has no horizontal overflow", async ({ page }) => {
    const response = await page.goto("/docs/api-reference");
    expect(response?.status()).toBe(200);
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    expect(await overflowsHorizontally(page)).toBe(false);
  });
});

test("version switcher reaches the archived v0.9 docs", async ({ page }) => {
  await page.goto("/docs/api-reference");
  await page.locator('button[aria-haspopup="dialog"]').filter({ hasText: "v1.0" }).click();
  await page.locator('a[href="/docs/v0.9"]').click();
  await expect(page).toHaveURL(/\/docs\/v0\.9$/);
  await expect(page.getByText("Archived documentation for v0.9").first()).toBeVisible();
});
