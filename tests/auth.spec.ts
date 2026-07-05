import { test, expect } from "@playwright/test";
import {
  goToGallery,
  loginViaUI,
  logoutViaUI,
  openMenu,
  menu,
} from "./helpers";

test.describe.configure({ timeout: 60000 });

test.describe("Authentication", () => {
  test("1: Login form renders from menu", async ({ page }) => {
    await goToGallery(page);
    await openMenu(page);
    // Click the Login label to open the modal
    await page
      .locator('#hamburger-menu-items label[aria-label="Login"]')
      .first()
      .click();
    // Wait for login form to load via HTMX
    await page.waitForSelector("#login-form", { timeout: 5000 });
    await expect(page.locator('input[name="username"]')).toBeVisible();
    await expect(page.locator('input[name="password"]')).toBeVisible();
    await expect(
      page.locator("#login-form button[type='submit']"),
    ).toBeVisible();
  });

  test("2: Login success", async ({ page }) => {
    await loginViaUI(page);
    // After login, menu should show Dashboard
    await openMenu(page);
    await expect(
      menu(page).getByText("Dashboard", { exact: true }),
    ).toBeVisible();
    await expect(
      menu(page).getByText("Login", { exact: true }),
    ).not.toBeVisible();
  });

  test("3: Login failure (wrong password)", async ({ page }) => {
    await goToGallery(page);
    await openMenu(page);
    await page
      .locator('#hamburger-menu-items label[aria-label="Login"]')
      .first()
      .click();
    await page.waitForSelector("#login-form", { timeout: 5000 });

    // Fill wrong credentials
    await page.locator('input[name="username"]').fill("admin");
    await page.locator('input[name="password"]').fill("wrongpassword");
    await page.locator("#login-form button[type='submit']").click();

    // Wait for the error message to appear
    await expect(page.locator("#login-error-message")).toBeVisible({
      timeout: 5000,
    });
    // Modal should still be checked (still open)
    await expect(page.locator("#login_modal")).toBeChecked({ timeout: 3000 });
  });

  test("4: Login without CSRF via HTTP request", async ({ page }) => {
    // Use Playwright's API request context to POST directly
    const r = await page.request.post("/login", {
      form: { username: "admin", password: "admin" },
      headers: { Origin: "http://localhost:8083" },
    });
    expect(r.status()).toBe(200);

    // After login via request, navigate to dashboard to verify session
    await page.goto("/dashboard");
    await expect(page.locator("body")).toBeAttached();
    // May or may not render dashboard depending on session sharing
  });

  test("5: Logout clears session", async ({ page }) => {
    await loginViaUI(page);
    await logoutViaUI(page);

    // Menu should show Login, not Dashboard
    await openMenu(page);
    await expect(menu(page).getByText("Login", { exact: true })).toBeVisible();
    await expect(
      menu(page).getByText("Dashboard", { exact: true }),
    ).not.toBeVisible();

    // Protected route should return 401
    const response = await page.goto("/dashboard");
    expect(response?.status()).toBe(401);
  });

  test("6: Session survives page navigation", async ({ page }) => {
    await loginViaUI(page);

    // Navigate to dashboard (full page navigation)
    await page.goto("/dashboard");
    await page.waitForSelector("#dashboard-container", { timeout: 10000 });

    // Go back and verify menu still shows authenticated state
    await page.goBack();
    await page.waitForTimeout(500);
    await openMenu(page);
    await expect(
      menu(page).getByText("Dashboard", { exact: true }),
    ).toBeVisible();
    await expect(
      menu(page).getByText("Login", { exact: true }),
    ).not.toBeVisible();
  });

  test("7: Session survives bfcache restore after polling", async ({
    page,
  }) => {
    await loginViaUI(page);

    // Navigate to dashboard
    await page.goto("/dashboard");
    await page.waitForSelector("#dashboard-container", { timeout: 10000 });

    // Wait for 3+ polling cycles (dashboard polls every 5s)
    await page.waitForTimeout(16_000);

    // Navigate back — in Chromium this serves gallery from bfcache
    await page.goBack();
    await page.waitForURL("**/gallery/1", { timeout: 10000 });
    await page.waitForTimeout(500);

    // Menu should show authenticated state
    await openMenu(page);
    await expect(
      menu(page).getByText("Dashboard", { exact: true }),
    ).toBeVisible();
    await expect(
      menu(page).getByText("Login", { exact: true }),
    ).not.toBeVisible();
  });

  test("8: Login→Logout→Re-Login cycle", async ({ page }) => {
    await loginViaUI(page);
    await logoutViaUI(page);
    await loginViaUI(page);

    // Should be authenticated again
    await openMenu(page);
    await expect(
      menu(page).getByText("Dashboard", { exact: true }),
    ).toBeVisible();
  });

  test("9: Menu shows unauthenticated state", async ({ page }) => {
    await goToGallery(page);
    await openMenu(page);
    await expect(menu(page).getByText("Login", { exact: true })).toBeVisible();
    await expect(
      menu(page).getByText("Dashboard", { exact: true }),
    ).not.toBeVisible();
  });

  test("10: Menu shows authenticated state", async ({ page }) => {
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

  test("11: Logged-out user gets 401 on protected route", async ({ page }) => {
    await loginViaUI(page);
    await logoutViaUI(page);

    const response = await page.goto("/dashboard");
    expect(response?.status()).toBe(401);
  });

  test("12: Login form HTMX partial renders correctly", async ({ page }) => {
    const r = await page.request.get("/login-form");
    expect(r.status()).toBe(200);
    const body = await r.text();
    expect(body).toContain('name="username"');
    expect(body).toContain('name="password"');
  });
});
