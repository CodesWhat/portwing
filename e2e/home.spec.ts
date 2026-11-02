// SPDX-License-Identifier: MIT

import { expect } from "@playwright/test";
import { MOBILE, overflowsHorizontally, test } from "./fixtures";

test.describe("home at 375px", () => {
  test.use({ viewport: MOBILE });

  test("mobile menu opens, closes on Escape and returns focus to the trigger", async ({ page }) => {
    await page.goto("/");
    const menu = page.locator("details.menu");
    const trigger = menu.locator("summary");

    await trigger.click();
    await expect(menu).toHaveJSProperty("open", true);
    await expect(menu.getByRole("link", { name: "Compare" })).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(menu).toHaveJSProperty("open", false);
    await expect(trigger).toBeFocused();
  });

  test("has no horizontal overflow", async ({ page }) => {
    await page.goto("/");
    await page.waitForLoadState("load");
    expect(await overflowsHorizontally(page)).toBe(false);
  });
});

test.describe("home on desktop", () => {
  test.use({ colorScheme: "dark" });

  test("theme toggle updates data-mode and aria-pressed and persists", async ({ page }) => {
    await page.goto("/");
    const html = page.locator("html");
    const toggle = page.locator("button.modetog");

    await expect(html).toHaveAttribute("data-mode", "dark");
    await expect(toggle).toHaveAttribute("aria-pressed", "true");

    await toggle.click();
    await expect(html).toHaveAttribute("data-mode", "light");
    await expect(toggle).toHaveAttribute("aria-pressed", "false");

    // The choice is stored, so it wins over the system scheme after a reload.
    await page.reload();
    await expect(html).toHaveAttribute("data-mode", "light");
    await expect(toggle).toHaveAttribute("aria-pressed", "false");

    await toggle.click();
    await expect(html).toHaveAttribute("data-mode", "dark");
    await expect(toggle).toHaveAttribute("aria-pressed", "true");
  });

  test("Cmd/Ctrl+K focuses the feature search", async ({ page }) => {
    await page.goto("/");
    const search = page.getByRole("searchbox", { name: "Search capabilities" });
    await expect(search).toBeVisible();
    await expect(search).not.toBeFocused();

    await page.keyboard.press("ControlOrMeta+k");
    await expect(search).toBeFocused();

    await search.fill("zzzz-no-such-capability");
    await expect(page.locator("#pempty")).toBeVisible();
  });

  test("terminal replay plays and reaches its done state", async ({ page }) => {
    await page.goto("/");
    const term = page.locator("#term");
    await term.scrollIntoViewIfNeeded();

    // Replaying a scene cancels the autoplay and runs just that one.
    const replay = term.getByRole("button", { name: "bad path" });
    await replay.click();
    await expect(replay).toHaveAttribute("aria-pressed", "true");
    await expect(term).toHaveAttribute("data-state", "playing");
    await expect(term).toHaveAttribute("data-state", "done", { timeout: 30_000 });
    expect(await term.locator("#term-body .tl").count()).toBeGreaterThan(1);
    await expect(term.locator(".caret")).toHaveCount(0);
  });
});

test.describe("home with reduced motion", () => {
  test.use({ reducedMotion: "reduce" });

  test("terminal shows its final content without playing", async ({ page }) => {
    await page.goto("/");
    const term = page.locator("#term");
    await expect(term).toHaveAttribute("data-state", "done");
    expect(await term.locator("#term-body .tl").count()).toBeGreaterThan(1);
    await expect(term.locator(".caret")).toHaveCount(0);
  });
});
