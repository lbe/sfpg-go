import { test, expect } from "@playwright/test";
import { loginViaUI, openMenu, menu } from "./helpers";

test.describe.configure({ timeout: 120000 });

// Server actions trigger background processing — use serial to avoid conflicts.
test.describe.serial("Server Actions", () => {
  test.beforeEach(async ({ page }) => {
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

    // Wait for toast/feedback in #server-toast-container
    await expect(page.locator("#server-toast-container")).toBeAttached({
      timeout: 5000,
    });
    // Discovery started should appear
    const toastText = await page
      .locator("#server-toast-container")
      .textContent();
    expect(toastText).not.toBeNull();
  });

  test("3: Run Cache Batch Load", async ({ page }) => {
    await openMenu(page);
    // Expand Server submenu
    await page.locator("#hamburger-menu-items details summary").click();
    await page.waitForTimeout(200);

    // Click Run Cache Batch Load
    await page.locator('button[aria-label="Run Cache Batch Load"]').click();

    // Wait for feedback in #server-toast-container
    await expect(page.locator("#server-toast-container")).toBeAttached({
      timeout: 5000,
    });
    const toastText = await page
      .locator("#server-toast-container")
      .textContent();
    expect(toastText).not.toBeNull();
    expect(toastText!.length).toBeGreaterThan(0);
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

  test("7: Server restart", async ({ page }) => {
    // Server restart is non-destructive — the process restarts in <10s.
    // We trigger restart via the UI (HTMX handles CSRF automatically).
    await openMenu(page);
    await page.locator('a[aria-label="Configuration"]').click();
    await page.waitForSelector("#config-form", { timeout: 5000 });

    // http-cache is in the Performance tab (not Server!). Switch tabs first.
    await page.locator("#tab-performance-btn").click();
    await page.waitForTimeout(200);

    const cacheCheckbox = page.locator('input[name="enable_http_cache"]');
    await expect(cacheCheckbox).toBeVisible({ timeout: 3000 });
    await cacheCheckbox.uncheck();

    // Save the config — this triggers the restart-required flow
    await page.locator("#config-form button[type='submit']").first().click();

    // The restart modal should appear (restart-diff-modal)
    const restartModalBtn = page.locator('button[hx-post="/config/restart"]');
    await expect(restartModalBtn).toBeVisible({ timeout: 5000 });
    await restartModalBtn.click({ force: true });

    // Wait for restart to begin
    await page.waitForTimeout(1500);

    // Poll health endpoint until server recovers (up to 20s)
    let recovered = false;
    for (let i = 0; i < 20; i++) {
      try {
        const resp = await page.request.get("/health", { timeout: 2000 });
        if (resp.status() === 200) {
          recovered = true;
          break;
        }
      } catch {
        // Server still restarting
      }
      await page.waitForTimeout(1000);
    }
    expect(recovered).toBeTruthy();

    // Verify the app is fully functional after restart
    await page.goto("/gallery/1");
    await expect(page.locator("#gallery-content")).toBeVisible({
      timeout: 10000,
    });
  });

  test("8: Unauthenticated access to server discovery", async ({ page }) => {
    // GET /server/discovery returns 400 (method not allowed — endpoint is POST-only)
    const response = await page.request.get("/server/discovery");
    expect(response.status()).toBe(400);
  });
});
