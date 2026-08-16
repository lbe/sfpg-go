import { test, expect } from "@playwright/test";
import { loginViaUI, openMenu, menu } from "./helpers";
import {
  enableHTTPCache,
  expectRestartDialogOpen,
  waitForServerRestart,
} from "./config-helpers";

test.describe.configure({ timeout: 120000 });

// Server actions trigger background processing — use serial to avoid conflicts.
test.describe.serial("Server Actions", () => {
  test.beforeEach(async ({ page }, testInfo) => {
    if (testInfo.title.startsWith("8:")) {
      return;
    }
    await loginViaUI(page);
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

  test("7: Server restart", async ({ page, request }) => {
    // Server restart is non-destructive — the process restarts in <10s.
    // We trigger restart via the UI (same-origin HTMX POST; COP at router).
    // The finally block restores the original config so the server is left
    // in a clean state.

    try {
      await openMenu(page);
      await page.locator('a[aria-label="Configuration"]').click();
      await page.waitForSelector("#config-form", { timeout: 5000 });

      // http-cache is in the Performance tab (not Server!). Switch tabs first.
      await page.locator("#tab-performance-btn").click();
      await page.waitForTimeout(200);

      const cacheCheckbox = page.locator('input[name="enable_http_cache"]');
      await expect(cacheCheckbox).toBeVisible({ timeout: 3000 });
      // Flip http-cache from its current state so the save is always a real
      // restart-required change (default http-cache is false, so a plain
      // uncheck would be a no-op).
      if (await cacheCheckbox.isChecked()) {
        await cacheCheckbox.uncheck();
      } else {
        await cacheCheckbox.check();
      }

      // Save the config — this triggers the restart-required flow
      await page.locator("#config-form button[type='submit']").first().click();

      // The restart modal should appear (restart-diff-modal). Assert the full
      // dialog-open contract: success alert with restart text, badge
      // visible/not hidden, modal toggle checked, and a non-empty diff table.
      await expectRestartDialogOpen(page);

      // Click Restart Server scoped to the open restart dialog (no force).
      const restartModalBtn = page.locator(
        'div.modal:has(#restart-diff-content) button[hx-post="/config/restart"]',
      );
      await expect(restartModalBtn).toBeVisible({ timeout: 5000 });
      await restartModalBtn.click();

      await waitForServerRestart(page.request);

      // Verify the app is fully functional after restart
      await page.goto("/gallery/1");
      await expect(page.locator("#gallery-content")).toBeVisible({
        timeout: 10000,
      });
    } finally {
      // Re-enable HTTP cache so the server is left in a clean state for
      // subsequent tests and the dev session.
      await enableHTTPCache(request);
    }
  });

  test("8: Unauthenticated access to server discovery", async ({ page }) => {
    // GET /server/discovery returns 400 (method not allowed — endpoint is POST-only)
    const response = await page.request.get("/server/discovery");
    expect(response.status()).toBe(400);
  });
});
