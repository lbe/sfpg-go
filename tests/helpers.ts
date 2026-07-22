import { type Page, type Locator, expect } from "@playwright/test";

// ── Menu ──────────────────────────────────────────────────────────

/** Click the hamburger button and wait for the dropdown to become visible. */
export async function openMenu(page: Page): Promise<void> {
  await page.locator("#hamburger-menu-btn").click();
  await expect(page.locator("#hamburger-menu-items")).toBeVisible();
}

/** Return the locator scoped to the hamburger menu's `<ul>`. */
export function menu(page: Page): Locator {
  return page.locator("#hamburger-menu-items");
}

// ── Auth ──────────────────────────────────────────────────────────

/**
 * Full login flow via the UI (not HTTP helpers) so we are testing the
 * exact same interaction a real user performs.
 *
 * 1. Navigate to gallery  → establishes a session
 * 2. Open hamburger menu  → click Login label → opens login modal
 * 3. Fill username / password → submit
 * 4. Wait for auth-changed → modal closes via hyperscript
 * 5. Wait for HTMX menu refresh (a[aria-label="Dashboard"] appears)
 */
export async function loginViaUI(page: Page): Promise<void> {
  await page.goto("/gallery/1");
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

  // Wait for the actual POST /login response (event-driven, no fixed timer).
  // A successful login returns HX-Trigger: auth-changed, which the global
  // handler in layout.html.tmpl uses to close the modal.
  const loginResponse = page.waitForResponse(
    (resp) =>
      resp.url().includes("/login") && resp.request().method() === "POST",
  );

  await page.locator("#login-form button[type='submit']").click();

  const response = await loginResponse;
  expect(response.status()).toBe(200);
  expect(response.headers()["hx-trigger"]).toBe("auth-changed");
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
/**
 * Full logout flow via the UI.
 *
 * 1. Open menu → click Logout → confirm
 * 2. Wait for auth-changed → menu refresh removes authenticated items
 */
export async function logoutViaUI(page: Page): Promise<void> {
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

// ── Navigation ────────────────────────────────────────────────────

/** Navigate to a gallery page and wait for content to render. */
export async function goToGallery(page: Page, id: number = 1): Promise<void> {
  await page.goto(`/gallery/${id}`);
  await page.waitForSelector("#gallery-content", { timeout: 10000 });
}

/** Navigate to the dashboard and wait for it to render. */
export async function goToDashboard(page: Page): Promise<void> {
  await page.goto("/dashboard");
  await page.waitForSelector("#dashboard-container", { timeout: 10000 });
  await page.waitForTimeout(300); // let hyperscript init run
}
