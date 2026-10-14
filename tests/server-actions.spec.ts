import { test, expect } from "@playwright/test";
import {
  ensureGallerySession,
  expandServerSubmenu,
  openMenu,
  menu,
} from "./helpers";

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
    await expandServerSubmenu(page);

    await expect(
      page.locator('button[aria-label="Run Cache Batch Load"]'),
    ).toBeVisible();
  });

  test("2: Run Discovery", async ({ page }) => {
    await openMenu(page);
    await expandServerSubmenu(page);

    await page.locator('button[aria-label="Run Discovery"]').click();

    await expect(page.locator("#discovery-started-toast")).toBeVisible({
      timeout: 10000,
    });
    await expect(page.locator("#server-toast-container")).not.toBeEmpty({
      timeout: 5000,
    });
  });

  test("3: Run Cache Batch Load", async ({ page }) => {
    await openMenu(page);
    await expandServerSubmenu(page);

    await page.locator('button[aria-label="Run Cache Batch Load"]').click();

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
    await expandServerSubmenu(page);

    const discoveryBtn = page.locator('button[aria-label="Run Discovery"]');

    await discoveryBtn.click();
    await discoveryBtn.click();

    await expect(page.locator("#server-toast-container")).toBeAttached({
      timeout: 5000,
    });
  });

  test("5: Session survives server actions", async ({ page }) => {
    await openMenu(page);
    await expandServerSubmenu(page);
    await page.locator('button[aria-label="Run Discovery"]').click();

    await expect(page.locator("#discovery-started-toast")).toBeVisible({
      timeout: 10000,
    });

    await openMenu(page);

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
    const response = await page.request.get("/server/discovery");
    expect(response.status()).toBe(400);
  });
});
