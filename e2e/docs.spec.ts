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

  test("mobile drawer carries the theme toggle and the header has none", async ({ page }) => {
    await page.goto("/docs");
    await expect(page.locator("#top button.modetog")).toHaveCount(0);
    await expect(page.locator("#top")).not.toContainText("Compare");
    await page.locator("#top details.menu summary").click();
    await expect(
      page.locator("#top details.menu").getByRole("link", { name: "Compare" }),
    ).toHaveCount(0);

    await page.keyboard.press("Escape");

    await page.locator("#nd-subnav").getByRole("button", { name: "Open Sidebar" }).click();
    const toggle = page.locator("#nd-sidebar-mobile button.modetog");
    const html = page.locator("html");
    await expect(toggle).toBeVisible();
    const before = await html.getAttribute("data-mode");
    await toggle.click();
    await expect(html).not.toHaveAttribute("data-mode", before ?? "");
  });

  test("api-reference has no horizontal overflow", async ({ page }) => {
    const response = await page.goto("/docs/api-reference");
    expect(response?.status()).toBe(200);
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    expect(await overflowsHorizontally(page)).toBe(false);
  });
});

test.describe("docs on desktop", () => {
  test.use({ colorScheme: "dark" });

  test("theme toggle lives in the sidebar, updates data-mode and aria-pressed and stores the choice", async ({
    page,
  }) => {
    await page.goto("/docs");
    const html = page.locator("html");
    const toggle = page.locator("#nd-sidebar button.modetog");
    await expect(page.locator("#top button.modetog")).toHaveCount(0);
    await expect(page.locator("#top")).not.toContainText("Compare");

    await expect(html).toHaveAttribute("data-mode", "dark");
    await expect(toggle).toHaveAttribute("aria-pressed", "true");

    await toggle.click();
    await expect(html).toHaveAttribute("data-mode", "light");
    await expect(toggle).toHaveAttribute("aria-pressed", "false");
    expect(await page.evaluate(() => localStorage.getItem("theme"))).toBe("light");
  });

  test("the TOC rail shows at 1440 and the layout has no horizontal overflow", async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/docs/api-reference");
    await expect(page.locator("#nd-toc")).toBeVisible();
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
