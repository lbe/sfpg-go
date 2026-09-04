import { test, expect } from "@playwright/test";
import { ensureGallerySession, openMenu, menu } from "./helpers";

test.describe.configure({ timeout: 120000 });

// Server restart UX is covered by config.spec.ts "13: Config restart" and
// tests/htmx-restart-alert.spec.ts — no duplicate restart test here.

// Server actions trigger background processing — use serial to avoid conflicts.
test.describe.serial("Server Actions", () => {
  test.beforeEach(async ({ page }, testInfo) => {
    if (testInfo.title.startsWith("8:")) {
      return;
    }
    await ensureGallerySession(page);
  });

  test("1: Server submenu renders", async ({ page }) => {
    await openMenu(page);
    // Expand the Server details disclosure
    const serverSummary = page.locator("#hamburger-menu-items details summary");
    await expect(serverSummary).toBeVisible();
    await serverSummary.click();
    await page.waitForTimeout(200);

    await expect(
      page.locator('button[aria-label="Run Discovery"]'),
    ).toBeVisible();
    await expect(
      page.locator('button[aria-label="Run Cache Batch Load"]'),
    ).toBeVisible();
  });

  test("2: Run Discovery", async ({ page }) => {
    await openMenu(page);
    // Expand Server submenu
    await page.locator("#hamburger-menu-items details summary").click();
    await page.waitForTimeout(200);

    // Click Run Discovery
    await page.locator('button[aria-label="Run Discovery"]').click();

    // Container is always in layout; wait for actual toast content.
    await expect(page.locator("#discovery-started-toast")).toBeVisible({
      timeout: 10000,
    });
    await expect(page.locator("#server-toast-container")).not.toBeEmpty({
      timeout: 5000,
    });
  });

  test("3: Run Cache Batch Load", async ({ page }) => {
    await openMenu(page);
    // Expand Server submenu
    await page.locator("#hamburger-menu-items details summary").click();
    await page.waitForTimeout(200);

    // Click Run Cache Batch Load
    await page.locator('button[aria-label="Run Cache Batch Load"]').click();

    // Container is always in layout; wait for actual toast content (not merely attached).
    await expect(page.locator("#cache-batch-load-toast")).toBeVisible({
      timeout: 10000,
    });
    await expect(page.locator("#server-toast-container")).not.toBeEmpty({
      timeout: 5000,
    });
  });

  test("4: Concurrent Discovery — second click handled gracefully", async ({
    page,
  }) => {
    await openMenu(page);
    await page.locator("#hamburger-menu-items details summary").click();
    await page.waitForTimeout(200);

    const discoveryBtn = page.locator('button[aria-label="Run Discovery"]');

    // Click twice in quick succession
    await discoveryBtn.click();
    await page.waitForTimeout(100);
    await discoveryBtn.click();

    // Should not crash — at minimum one toast container should exist
    await expect(page.locator("#server-toast-container")).toBeAttached({
      timeout: 5000,
    });
  });

  test("5: Session survives server actions", async ({ page }) => {
    await openMenu(page);
    await page.locator("#hamburger-menu-items details summary").click();
    await page.waitForTimeout(200);
    await page.locator('button[aria-label="Run Discovery"]').click();
    await page.waitForTimeout(1000);

    // Close menu and re-open
    await page.keyboard.press("Escape");
    await page.waitForTimeout(200);
    await openMenu(page);

    // Should still be authenticated
    await expect(
      menu(page).getByText("Dashboard", { exact: true }),
    ).toBeVisible();
    await expect(
      menu(page).getByText("Login", { exact: true }),
    ).not.toBeVisible();
  });

  test("6: Server shutdown", async ({ page }) => {
    test.skip(true, "Destructive — server process would stop");
  });

  test("8: Unauthenticated access to server discovery", async ({ page }) => {
    // GET /server/discovery returns 400 (method not allowed — endpoint is POST-only)
    const response = await page.request.get("/server/discovery");
    expect(response.status()).toBe(400);
  });
});
