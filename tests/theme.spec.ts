import { test, expect } from "@playwright/test";
import { goToGallery, openMenu } from "./helpers";

test.describe.configure({ timeout: 60000 });

test.describe("Theme", () => {
  test("1: Theme modal opens from menu", async ({ page }) => {
    await goToGallery(page);
    await openMenu(page);

    // Click the Theme label
    await page.locator('#hamburger-menu-items a[aria-label="Theme"]').click();
    await page.waitForTimeout(200);

    // Wait for theme modal to open
    await expect(page.locator("#theme_modal")).toBeChecked({ timeout: 5000 });
    // At least one theme card should be visible
    await expect(page.locator(".theme-card").first()).toBeVisible({
      timeout: 5000,
    });
  });

  test("2: Switch to dark theme", async ({ page }) => {
    await goToGallery(page);
    await openMenu(page);
    await page.locator('#hamburger-menu-items a[aria-label="Theme"]').click();
    await page.waitForTimeout(200);
    await expect(page.locator("#theme_modal")).toBeChecked({ timeout: 5000 });

    // Click the dark theme card
    await page.locator('.theme-card[data-theme="dark"]').click();
    await page.waitForTimeout(100);

    // Check the hidden input reflects selection
    const selectedValue = await page
      .locator("#theme-selected-value")
      .inputValue();
    expect(selectedValue).toBe("dark");

    // Click Apply Theme
    await page.locator("#theme-apply-btn").click();

    // Apply Theme reloads the page — wait for load + theme attribute
    await page.waitForLoadState("domcontentloaded", { timeout: 15000 });
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark", {
      timeout: 5000,
    });
  });

  test("3: Switch to light theme", async ({ page }) => {
    // Visit /login-form first to establish a session with a fresh CSRF
    // token.  /gallery/1 is HTTP-cached and may serve a stale CSRF token
    // from a preload session, causing theme POSTs to fail after WP-12.
    await page.goto("/login-form");
    // Start from dark theme state
    await goToGallery(page);
    await openMenu(page);
    await page.locator('#hamburger-menu-items a[aria-label="Theme"]').click();
    await page.waitForTimeout(200);
    await expect(page.locator("#theme_modal")).toBeChecked({ timeout: 5000 });

    // Click light theme
    await page.locator('.theme-card[data-theme="light"]').click();
    await page.waitForTimeout(100);
    await page.locator("#theme-apply-btn").click();

    await page.waitForLoadState("domcontentloaded", { timeout: 15000 });
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light", {
      timeout: 5000,
    });
  });

  test("4: Theme persists on reload", async ({ page }) => {
    // First set to dark
    await goToGallery(page);
    // nWait for HTMX menu fetch to settle
    await page.waitForLoadState("networkidle", { timeout: 10000 });
    await openMenu(page);
    await page.locator('#hamburger-menu-items a[aria-label="Theme"]').click();
    await page.waitForTimeout(200);
    await expect(page.locator("#theme_modal")).toBeChecked({ timeout: 5000 });
    await page.locator('.theme-card[data-theme="dark"]').click();
    await page.locator("#theme-apply-btn").click();
    await page.waitForLoadState("domcontentloaded", { timeout: 15000 });
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark", {
      timeout: 5000,
    });

    // Reload
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark", {
      timeout: 5000,
    });

    // Restore light for other tests — wait for idle after goToGallery
    await goToGallery(page);
    await page.waitForLoadState("networkidle", { timeout: 10000 });
    await openMenu(page);
    await page.locator('#hamburger-menu-items a[aria-label="Theme"]').click();
    await page.waitForTimeout(200);
    await page.locator('.theme-card[data-theme="light"]').click();
    await page.locator("#theme-apply-btn").click();
    await page.waitForLoadState("domcontentloaded", { timeout: 15000 });
  });

  test("5: Theme persists across navigation", async ({ page }) => {
    // Set to dark
    await goToGallery(page);
    await openMenu(page);
    await page.locator('#hamburger-menu-items a[aria-label="Theme"]').click();
    await page.waitForTimeout(200);
    await page.locator('.theme-card[data-theme="dark"]').click();
    await page.locator("#theme-apply-btn").click();
    await page.waitForLoadState("domcontentloaded", { timeout: 15000 });

    // Navigate to a different gallery
    await page.goto("/gallery/1");
    await page.waitForSelector("#gallery-content", { timeout: 10000 });
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark", {
      timeout: 5000,
    });

    // Restore light — wait for network idle so HTMX pageshow fetch completes
    // before opening the dropdown (avoids race where innerHTML swap detaches the <a>)
    await page.waitForLoadState("networkidle", { timeout: 10000 });
    await openMenu(page);
    await page.locator('#hamburger-menu-items a[aria-label="Theme"]').click();
    await page.waitForTimeout(200);
    await page.locator('.theme-card[data-theme="light"]').click();
    await page.locator("#theme-apply-btn").click();
    await page.waitForLoadState("domcontentloaded", { timeout: 15000 });
  });

  test("6: Theme works without auth", async ({ page }) => {
    // Go to gallery without logging in
    await goToGallery(page);
    await openMenu(page);

    // Wait for the theme link to be fully stable (not mid-HTMX-swap)
    const themeLink = page.locator(
      '#hamburger-menu-items a[aria-label="Theme"]',
    );
    await expect(themeLink).toBeVisible({ timeout: 5000 });
    await themeLink.click();
    await page.waitForTimeout(200);
    await expect(page.locator("#theme_modal")).toBeChecked({ timeout: 5000 });
    await page.locator('.theme-card[data-theme="dark"]').click();
    await page.locator("#theme-apply-btn").click();
    await page.waitForLoadState("domcontentloaded", { timeout: 15000 });
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark", {
      timeout: 5000,
    });
    // No restore needed — this is the last test
  });
});
