import { test, expect } from "@playwright/test";
import {
  goToGallery,
  guestStorageState,
  openMenu,
  openThemeModal,
  selectThemeCard,
} from "./helpers";

test.describe.configure({ timeout: 60000 });

test.describe("Theme", () => {
  test("1: Theme modal opens from menu", async ({ page }) => {
    await goToGallery(page);
    await openMenu(page);
    await openThemeModal(page);
  });

  test("2: Switch to dark theme", async ({ page }) => {
    await goToGallery(page);
    await openMenu(page);
    await openThemeModal(page);

    await selectThemeCard(page, "dark");

    await page.locator("#theme-apply-btn").click();

    await page.waitForLoadState("domcontentloaded", { timeout: 15000 });
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark", {
      timeout: 5000,
    });
  });

  test("3: Switch to light theme", async ({ page }) => {
    await goToGallery(page);
    await openMenu(page);
    await openThemeModal(page);

    await selectThemeCard(page, "light");
    await page.locator("#theme-apply-btn").click();

    await page.waitForLoadState("domcontentloaded", { timeout: 15000 });
    await expect(page.locator("html")).toHaveAttribute("data-theme", "light", {
      timeout: 5000,
    });
  });

  test("4: Theme persists on reload", async ({ page }) => {
    await goToGallery(page);
    await openMenu(page);
    await openThemeModal(page);
    await selectThemeCard(page, "dark");
    await page.locator("#theme-apply-btn").click();
    await page.waitForLoadState("domcontentloaded", { timeout: 15000 });
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark", {
      timeout: 5000,
    });

    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark", {
      timeout: 5000,
    });

    await goToGallery(page);
    await openMenu(page);
    await openThemeModal(page);
    await selectThemeCard(page, "light");
    await page.locator("#theme-apply-btn").click();
    await page.waitForLoadState("domcontentloaded", { timeout: 15000 });
  });

  test("5: Theme persists across navigation", async ({ page }) => {
    await goToGallery(page);
    await openMenu(page);
    await openThemeModal(page);
    await selectThemeCard(page, "dark");
    await page.locator("#theme-apply-btn").click();
    await page.waitForLoadState("domcontentloaded", { timeout: 15000 });

    await page.goto("/gallery/1");
    await page.waitForSelector("#gallery-content", { timeout: 10000 });
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark", {
      timeout: 5000,
    });

    await openMenu(page);
    await openThemeModal(page);
    await selectThemeCard(page, "light");
    await page.locator("#theme-apply-btn").click();
    await page.waitForLoadState("domcontentloaded", { timeout: 15000 });
  });

  test.describe("guest", () => {
    test.use({ storageState: guestStorageState });

    test("6: Theme works without auth", async ({ page }) => {
      await goToGallery(page);
      await openMenu(page);

      const themeLink = page.locator(
        '#hamburger-menu-items a[aria-label="Theme"]',
      );
      await expect(themeLink).toBeVisible({ timeout: 5000 });
      await openThemeModal(page);
      await selectThemeCard(page, "dark");
      await page.locator("#theme-apply-btn").click();
      await page.waitForLoadState("domcontentloaded", { timeout: 15000 });
      await expect(page.locator("html")).toHaveAttribute("data-theme", "dark", {
        timeout: 5000,
      });
    });
  });
});
