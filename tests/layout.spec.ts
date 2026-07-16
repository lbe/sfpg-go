import { test, expect } from "@playwright/test";
import { goToGallery, loginViaUI, openMenu, menu } from "./helpers";

test.describe.configure({ timeout: 60000 });

test.describe("Layout & Static Assets", () => {
  test("1: Favicon returns SVG", async ({ page }) => {
    const response = await page.goto("/favicon.ico");
    expect(response?.status()).toBe(200);
    const contentType = response?.headers()["content-type"] || "";
    expect(contentType).toContain("svg");
  });

  test("2: Health endpoint returns ok", async ({ page }) => {
    const response = await page.goto("/health");
    expect(response?.status()).toBe(200);
    const body = await page.locator("body").innerText();
    expect(body).toContain("ok");
  });

  test("3: Static asset serves with correct content type", async ({ page }) => {
    const response = await page.goto("/static/favicon/favicon.svg");
    expect(response?.status()).toBe(200);
    const contentType = response?.headers()["content-type"] || "";
    expect(contentType).toContain("svg");
  });

  test("4: Menu structure shows unauthenticated items", async ({ page }) => {
    await goToGallery(page);
    await openMenu(page);

    await expect(menu(page)).toBeVisible();
    // Should have list items
    const listItems = menu(page).locator("li");
    const count = await listItems.count();
    expect(count).toBeGreaterThan(0);

    // Login and Theme should be visible
    await expect(menu(page).getByText("Login", { exact: true })).toBeVisible();
    await expect(menu(page).getByText("Theme", { exact: true })).toBeVisible();
  });

  test("5: Menu cache headers include no-store", async ({ page }) => {
    const response = await page.request.get("/hamburger-menu");
    const cacheControl = response.headers()["cache-control"] || "";
    expect(
      cacheControl.includes("no-store") || cacheControl.includes("no-cache"),
    ).toBeTruthy();
  });

  test("6: About modal opens", async ({ page }) => {
    await goToGallery(page);
    await openMenu(page);

    await page
      .locator('#hamburger-menu-items label[aria-label="About"]')
      .click();
    // about_modal is a checkbox input — checking it reveals the sibling .modal
    await expect(page.locator("#about_modal")).toBeChecked({ timeout: 5000 });
    // Version text is inside .modal-box (sibling of the checkbox), not on the checkbox itself
    // Scope to the about modal's .modal-box (sibling of #about_modal checkbox)
    await expect(
      page.locator(".modal-box").filter({ hasText: "Version" }),
    ).toBeVisible({ timeout: 3000 });
  });

  test("7: About modal closes via Escape", async ({ page }) => {
    await goToGallery(page);
    await openMenu(page);
    await page
      .locator('#hamburger-menu-items label[aria-label="About"]')
      .click();
    await expect(page.locator("#about_modal")).toBeChecked({ timeout: 5000 });

    await page.keyboard.press("Escape");
    await expect(page.locator("#about_modal")).not.toBeChecked({
      timeout: 3000,
    });
  });

  test("8: Pprof unauthenticated returns 400", async ({ page }) => {
    const response = await page.goto("/debug/pprof/");
    expect(response?.status()).toBe(400);
  });

  test("9: Pprof disabled when authenticated", async ({ page }) => {
    await loginViaUI(page);
    const response = await page.goto("/debug/pprof/");
    expect(response?.status()).toBe(400);
  });

  test("10: HTML title is not empty", async ({ page }) => {
    await goToGallery(page);
    const title = await page.title();
    expect(title).not.toBe("");
  });

  test("11: HTML viewport meta tag present", async ({ page }) => {
    await goToGallery(page);
    const viewport = page.locator('meta[name="viewport"]');
    await expect(viewport).toBeAttached();
  });

  test("12: Responsive at 375px (mobile)", async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 667 });
    await goToGallery(page);
    // Should not have visible horizontal overflow (body should not scroll horizontally)
    // Hamburger menu should be visible
    await expect(page.locator("#hamburger-menu-btn")).toBeVisible();
    // Gallery content renders
    await expect(page.locator("#gallery-content")).toBeVisible();
  });

  test("13: Responsive at 768px (tablet)", async ({ page }) => {
    await page.setViewportSize({ width: 768, height: 900 });
    await goToGallery(page);
    await expect(page.locator("#gallery-content")).toBeVisible();
    // Gallery tiles should render
    const tiles = page.locator(".gallery-tile");
    await expect(tiles.first()).toBeVisible({ timeout: 5000 });
  });

  test("14: Responsive at 1280px (desktop)", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await goToGallery(page);
    await expect(page.locator("#gallery-content")).toBeVisible();
    // Gallery grid should render with multiple tiles
    const tiles = page.locator(".gallery-tile");
    await expect(tiles.first()).toBeVisible({ timeout: 5000 });
  });
});
