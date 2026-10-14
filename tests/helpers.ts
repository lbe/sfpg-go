import { type Page, type Locator, expect } from "@playwright/test";

/** Use with test.use() for login/logout and unauthenticated contract tests. */
export const guestStorageState = { cookies: [], origins: [] };

// Dashboard polls every 5s via HTMX; #last-updated changes on each refresh.
const DASHBOARD_POLL_TIMEOUT_MS = 15_000;

// ── Menu ──────────────────────────────────────────────────────────

/** Click the hamburger button and wait for the dropdown to become visible. */
export async function openMenu(page: Page): Promise<void> {
  const btn = page.locator("#hamburger-menu-btn");
  const items = page.locator("#hamburger-menu-items");
  if (!(await items.isVisible())) {
    await btn.click();
  }
  await expect(items).toBeVisible();
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
  await ensureMenuClosed(page);
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
  await waitForDashboardShell(page);
}

/** Close the hamburger dropdown if it is open (Escape does not always dismiss daisyUI). */
export async function ensureMenuClosed(page: Page): Promise<void> {
  const btn = page.locator("#hamburger-menu-btn");
  const items = page.locator("#hamburger-menu-items");
  if (await items.isVisible()) {
    await btn.click();
  }
  if (await items.isVisible()) {
    await page.locator("#gallery-content").click({ position: { x: 8, y: 8 } });
  }
  await expect(items).toBeHidden({ timeout: 5000 });
}

/** Dashboard layout and poller target are present. */
export async function waitForDashboardShell(page: Page): Promise<void> {
  await expect(page.locator("#dashboard-container")).toBeVisible({
    timeout: 10000,
  });
  await expect(page.locator("#last-updated")).toBeVisible({ timeout: 10000 });
}

/** Gallery content and authenticated menu item after navigation / bfcache. */
export async function waitForAuthenticatedGalleryShell(
  page: Page,
): Promise<void> {
  await expect(page.locator("#gallery-content")).toBeVisible({
    timeout: 10000,
  });
  await expect(
    page.locator("#hamburger-menu-items a[aria-label='Dashboard']"),
  ).toBeAttached({ timeout: 10000 });
}

/** Open theme modal and wait for theme cards. */
export async function openThemeModal(page: Page): Promise<void> {
  await page.locator('#hamburger-menu-items a[aria-label="Theme"]').click();
  await expect(page.locator("#theme_modal")).toBeChecked({ timeout: 5000 });
  await expect(page.locator(".theme-card").first()).toBeVisible({
    timeout: 5000,
  });
}

/** Expand Server submenu in the hamburger menu. */
export async function expandServerSubmenu(page: Page): Promise<void> {
  const summary = page.locator("#hamburger-menu-items details summary");
  await summary.click();
  await expect(page.locator('button[aria-label="Run Discovery"]')).toBeVisible({
    timeout: 5000,
  });
}

/** Topmost config modal (HTMX appends beforeend; duplicates are a bug). */
export function configModal(page: Page): Locator {
  return page.locator("#config-modal-container").last();
}

/** Wait until success-alert auto-close removed the config modal from the DOM. */
export async function waitForConfigModalDismissed(page: Page): Promise<void> {
  await expect(page.locator("#config-modal-container")).toHaveCount(0, {
    timeout: 8000,
  });
}

/** Open Configuration from the menu and wait for the modal shell. */
export async function openConfigModalFromMenu(page: Page): Promise<Locator> {
  await openMenu(page);
  await page.locator('a[aria-label="Configuration"]').click();
  const root = configModal(page);
  await expect(root.locator("#config-form")).toBeVisible({ timeout: 5000 });
  await expect(root.locator("#config_modal")).toBeChecked({ timeout: 5000 });
  return root;
}

/** Click a config tab and wait for a field on that tab. */
export async function openConfigTab(
  page: Page,
  tabButtonSelector: string,
  fieldSelector: string,
): Promise<void> {
  const root = configModal(page);
  const tab = root.locator(tabButtonSelector);
  await tab.scrollIntoViewIfNeeded();
  await tab.click();
  await expect(root.locator(fieldSelector)).toBeVisible({ timeout: 5000 });
}

/** Show the info box sidebar if it is hidden. */
export async function ensureInfoBoxWrapperVisible(page: Page): Promise<void> {
  const wrapper = page.locator("#box_info_wrapper");
  if (await wrapper.evaluate((el) => el.classList.contains("hidden"))) {
    await page.locator("#info-btn").click();
  }
  await expect(wrapper).not.toHaveClass(/hidden/, { timeout: 5000 });
}

/** Select a theme card and wait for the hidden input to match. */
export async function selectThemeCard(
  page: Page,
  theme: "dark" | "light",
): Promise<void> {
  await page.locator(`.theme-card[data-theme="${theme}"]`).click();
  await expect(page.locator("#theme-selected-value")).toHaveValue(theme, {
    timeout: 5000,
  });
}

/** Land on gallery; relies on project storageState (setup auth) when present. */
export async function ensureGallerySession(page: Page): Promise<void> {
  await page.goto("/gallery/1");
  await page.waitForSelector("#gallery-content", { timeout: 10000 });
}

/** Wait until #last-updated text differs from `from` (one poll cycle). */
export async function waitForLastUpdatedChange(
  page: Page,
  from: string | null,
): Promise<string> {
  const locator = page.locator("#last-updated");
  let changed = "";
  await expect
    .poll(
      async () => {
        const cur = await locator.textContent();
        if (cur && cur !== from) {
          changed = cur;
          return true;
        }
        return false;
      },
      { timeout: DASHBOARD_POLL_TIMEOUT_MS },
    )
    .toBe(true);
  return changed;
}

/** Wait for N dashboard poll cycles (last-updated changes N times). */
export async function waitForDashboardPollCycles(
  page: Page,
  cycles: number,
): Promise<void> {
  let prev = await page.locator("#last-updated").textContent();
  for (let i = 0; i < cycles; i++) {
    prev = await waitForLastUpdatedChange(page, prev);
  }
}
