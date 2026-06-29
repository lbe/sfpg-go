import { test, expect, Page } from "@playwright/test";

const BASE_URL = "http://localhost:8083";

// Some tests involve 16s polling waits; accommodate that globally.
test.describe.configure({ timeout: 60000 });

// ───────────────────────────────────────────────────────────────────
// Helpers
// ───────────────────────────────────────────────────────────────────

/** Click the hamburger button to open (or close) the DaisyUI dropdown. */
async function openMenu(page: Page) {
  await page.locator("#hamburger-menu-btn").click();
  await page.waitForTimeout(150);
}

/**
 * Full login flow via the UI (not HTTP helpers) so we are testing the
 * exact same interaction a real user performs.
 *
 * 1. Navigate to gallery  → establishes a session and gets a CSRF token
 * 2. Open hamburger menu  → click Login label → opens login modal
 * 3. Fill username / password → submit
 * 4. Wait for auth-changed → modal closes via hyperscript
 * 5. Wait for HTMX menu refresh (a[aria-label="Dashboard"] appears)
 */
async function loginViaUI(page: Page) {
  await page.goto(BASE_URL + "/gallery/1");
  await page.waitForSelector("#gallery-content", { timeout: 10000 });

  await openMenu(page);
  // Use aria-label + scope to avoid matching the modal-backdrop label
  await page
    .locator('#hamburger-menu-items label[aria-label="Login"]')
    .first()
    .click();

  // Wait for login form loaded via HTMX into the modal
  await page.waitForSelector("#login-form", { timeout: 5000 });

  await page.locator('input[name="username"]').fill("admin");
  await page.locator('input[name="password"]').fill("admin");
  await page.locator("#login-form button[type='submit']").click();

  // The hyperscript handler: on auth-changed from body → wait 50ms → uncheck modal
  await expect(page.locator("#login_modal")).not.toBeChecked({ timeout: 5000 });

  // Wait for the HTMX menu refresh triggered by auth-changed event
  // Check for DOM attachment — the menu items are inside a hidden dropdown
  await page.waitForSelector(
    "#hamburger-menu-items a[aria-label='Dashboard']",
    { state: "attached", timeout: 5000 },
  );
}

/**
 * Full logout flow via the UI.
 *
 * 1. Open menu → click Logout → confirm
 * 2. Wait for auth-changed → menu refresh removes authenticated items
 */
async function logoutViaUI(page: Page) {
  await openMenu(page);
  await page
    .locator('#hamburger-menu-items label[aria-label="Logout"]')
    .click();
  await page.waitForSelector("#logout-form", { timeout: 3000 });
  await page.locator("#logout-form button[type='submit']").click();

  // Wait for HTMX menu refresh: authenticated items (Dashboard) are detached
  // Wait for auth-changed → menu refresh (Dashboard removed from DOM)
  await page.waitForSelector(
    "#hamburger-menu-items a[aria-label='Dashboard']",
    {
      state: "detached",
      timeout: 5000,
    },
  );
  // Close the dropdown by pressing Escape so openMenu works fresh in callers
  await page.keyboard.press("Escape");
  await page.waitForTimeout(100);
}

/** Return the locator scoped to the hamburger menu's `<ul>`. */
function menu(page: Page) {
  return page.locator("#hamburger-menu-items");
}

// ───────────────────────────────────────────────────────────────────
// Tests
// ───────────────────────────────────────────────────────────────────

test("Health: server is reachable", async ({ page }) => {
  const response = await page.goto(BASE_URL + "/health");
  expect(response?.status()).toBe(200);
});

test("Test 1: Menu shows unauthenticated state", async ({ page }) => {
  await page.goto(BASE_URL + "/gallery/1");
  await page.waitForSelector("#gallery-content", { timeout: 10000 });

  await openMenu(page);
  await expect(menu(page).getByText("Login", { exact: true })).toBeVisible();
  await expect(
    menu(page).getByText("Dashboard", { exact: true }),
  ).not.toBeVisible();
});

test("Test 2: Menu shows authenticated state after login", async ({ page }) => {
  await loginViaUI(page);

  await openMenu(page);
  await expect(menu(page).getByText("Theme", { exact: true })).toBeVisible();
  await expect(
    menu(page).getByText("Dashboard", { exact: true }),
  ).toBeVisible();
  await expect(
    menu(page).getByText("Configuration", { exact: true }),
  ).toBeVisible();
  await expect(menu(page).getByText("Logout", { exact: true })).toBeVisible();
  await expect(
    menu(page).getByText("Login", { exact: true }),
  ).not.toBeVisible();
});

test("Test 3: Session survives config modal open/cancel", async ({ page }) => {
  await loginViaUI(page);

  // Open menu → Configuration
  await openMenu(page);
  await page.locator('a[aria-label="Configuration"]').click();

  // Wait for config modal to be fully loaded
  await page.waitForSelector("#config-form", { timeout: 5000 });
  // The .modal-toggle checkbox is visually hidden; only check DOM attachment
  await page.waitForSelector("#config_modal:checked", {
    state: "attached",
    timeout: 5000,
  });

  // Click Cancel (no unsaved changes → modal closes immediately)
  await page.locator("#config-cancel-btn").click();
  await expect(page.locator("#config_modal")).not.toBeChecked({
    timeout: 3000,
  });

  // Give HTMX a moment (auth state unchanged but menu might have been touched)
  await page.waitForTimeout(200);

  // Menu should still show authenticated state
  await openMenu(page);
  await expect(
    menu(page).getByText("Dashboard", { exact: true }),
  ).toBeVisible();
  await expect(
    menu(page).getByText("Login", { exact: true }),
  ).not.toBeVisible();
});

test("Test 4: Session survives dashboard navigation", async ({ page }) => {
  await loginViaUI(page);

  // Full-page navigation to the dashboard
  await page.goto(BASE_URL + "/dashboard");
  await page.waitForSelector("#dashboard-container", { timeout: 10000 });

  // Menu on the dashboard page should reflect authenticated state
  await openMenu(page);
  await expect(
    menu(page).getByText("Dashboard", { exact: true }),
  ).toBeVisible();
  await expect(
    menu(page).getByText("Login", { exact: true }),
  ).not.toBeVisible();
});

test("Test 5: Session survives dashboard polling then back navigation (bfcache)", async ({
  page,
}) => {
  await loginViaUI(page);

  // Navigate to dashboard (bfcache now caches the gallery page)
  await page.goto(BASE_URL + "/dashboard");
  await page.waitForSelector("#dashboard-container", { timeout: 10000 });

  // Wait for 3 polling cycles (dashboard polls /dashboard every 5 s)
  // This exercises the scenario where polling could affect session state.
  await page.waitForTimeout(16_000);

  // Navigate back — in Chromium this serves the gallery page from bfcache
  await page.goBack();
  await page.waitForURL("**/gallery/1", { timeout: 10000 });

  // The pageshow event from bfcache triggers HTMX to reload the menu.
  await page.waitForTimeout(500);

  // Menu MUST show authenticated state (the core bfcache fix)
  await openMenu(page);
  await expect(
    menu(page).getByText("Dashboard", { exact: true }),
  ).toBeVisible();
  await expect(
    menu(page).getByText("Login", { exact: true }),
  ).not.toBeVisible();
});

test("Test 6: Server actions from menu don't invalidate session", async ({
  page,
}) => {
  await loginViaUI(page);
  await page.goto(BASE_URL + "/dashboard");
  await page.waitForSelector("#dashboard-container", { timeout: 10000 });

  // Open menu and expand the Server collapse
  await openMenu(page);
  await page.locator("#hamburger-menu-items details summary").click();
  await page.waitForTimeout(200);

  // Run Discovery
  await page.locator('button[aria-label="Run Discovery"]').click();
  await page.waitForTimeout(1000);

  // The Server submenu may still be open; click the second button directly.
  // (If toggled closed, reopen by clicking summary again.)
  const cacheBtn = page.locator('button[aria-label="Run Cache Batch Load"]');
  if (!(await cacheBtn.isVisible().catch(() => false))) {
    await page.locator("#hamburger-menu-items details summary").click();
    await page.waitForTimeout(200);
  }
  await cacheBtn.click();
  await page.waitForTimeout(1000);

  // Menu should still be authenticated
  // Close any open dropdown first to avoid toggle confusion
  await page.keyboard.press("Escape");
  await page.waitForTimeout(100);
  await openMenu(page);

  await expect(
    menu(page).getByText("Dashboard", { exact: true }),
  ).toBeVisible();
  await expect(
    menu(page).getByText("Login", { exact: true }),
  ).not.toBeVisible();
});

test("Test 7: Full user flow (exact reported scenario)", async ({ page }) => {
  // 1. Start unauthenticated on gallery
  await page.goto(BASE_URL + "/gallery/1");
  await page.waitForSelector("#gallery-content", { timeout: 10000 });

  // 2. Menu shows Login
  await openMenu(page);
  await expect(menu(page).getByText("Login", { exact: true })).toBeVisible();

  // 3. Login via menu
  await loginViaUI(page);

  // 4. Menu shows Dashboard
  await openMenu(page);
  await expect(
    menu(page).getByText("Dashboard", { exact: true }),
  ).toBeVisible();

  // 5. Configuration → Cancel
  await page.locator('a[aria-label="Configuration"]').click();
  await page.waitForSelector("#config-form", { timeout: 5000 });
  // The .modal-toggle checkbox is visually hidden; only check DOM attachment
  await page.waitForSelector("#config_modal:checked", {
    state: "attached",
    timeout: 5000,
  });
  await page.locator("#config-cancel-btn").click();
  await expect(page.locator("#config_modal")).not.toBeChecked({
    timeout: 3000,
  });
  await page.waitForTimeout(200);

  // 6. Navigate to Dashboard
  await page.goto(BASE_URL + "/dashboard");
  await page.waitForSelector("#dashboard-container", { timeout: 10000 });

  // 7. On Dashboard: Server → Run Discovery
  await openMenu(page);
  await page.locator("#hamburger-menu-items details summary").click();
  await page.waitForTimeout(200);
  await page.locator('button[aria-label="Run Discovery"]').click();
  await page.waitForTimeout(1000);

  // 8. Server → Run Cache Batch Load
  const cacheBtn = page.locator('button[aria-label="Run Cache Batch Load"]');
  if (!(await cacheBtn.isVisible().catch(() => false))) {
    await page.locator("#hamburger-menu-items details summary").click();
    await page.waitForTimeout(200);
  }
  await cacheBtn.click();
  await page.waitForTimeout(1000);

  // 9. Wait for dashboard polling to fire
  await page.waitForTimeout(6000);

  // 10. Browser back → gallery restores from bfcache
  await page.goBack();
  await page.waitForURL("**/gallery/1", { timeout: 10000 });
  await page.waitForTimeout(500);

  // 11. Menu MUST show Dashboard/Logout, NOT Login
  await openMenu(page);
  await expect(
    menu(page).getByText("Dashboard", { exact: true }),
  ).toBeVisible();
  await expect(
    menu(page).getByText("Login", { exact: true }),
  ).not.toBeVisible();
});

test("Test 8: Logout clears session", async ({ page }) => {
  await loginViaUI(page);

  // Logout via UI
  await logoutViaUI(page);
  await page.waitForTimeout(300);

  // Menu should show Login, not Dashboard
  await openMenu(page);
  await expect(menu(page).getByText("Login", { exact: true })).toBeVisible();
  await expect(
    menu(page).getByText("Dashboard", { exact: true }),
  ).not.toBeVisible();

  // Protected route should return 401
  const response = await page.goto(BASE_URL + "/dashboard");
  expect(response?.status()).toBe(401);
});
