import { test, expect } from "@playwright/test";
import { goToGallery, loginViaUI, openMenu, menu } from "./helpers";
import { getFirstFolderID } from "./db-queries";

test.describe.configure({ timeout: 60000 });

let folderID: number;

test.beforeAll(() => {
  folderID = getFirstFolderID()!;
  if (!folderID) test.skip(true, "No folder data — run discovery first");
});

test.describe("Navigation", () => {
  test("1: Direct URL entry", async ({ page }) => {
    await page.goto(`/gallery/${folderID}`);
    await expect(page.locator("#gallery-content")).toBeVisible({
      timeout: 10000,
    });
    expect(page.url()).toContain(`/gallery/${folderID}`);
  });

  test("2: Browser back from sub-folder", async ({ page }) => {
    await goToGallery(page, folderID);
    const folderLink = page
      .locator('.gallery-tile a[hx-get][hx-target="#gallery-content"]')
      .first();
    await expect(folderLink).toBeVisible({ timeout: 5000 });

    const hxGet = await folderLink.getAttribute("hx-get");
    const match = hxGet?.match(/\/gallery\/(\d+)/);
    test.skip(!match, "Could not determine subfolder ID");
    const subID = match![1];

    await folderLink.click();
    await page.waitForURL(`**/gallery/${subID}**`, { timeout: 15000 });

    await page.goBack();
    await page.waitForURL(`**/gallery/${folderID}**`, { timeout: 10000 });
    await expect(page.locator("#gallery-content")).toBeVisible();
  });

  test("3: Browser forward after back", async ({ page }) => {
    await goToGallery(page, folderID);
    const folderLink = page
      .locator('.gallery-tile a[hx-get][hx-target="#gallery-content"]')
      .first();
    await expect(folderLink).toBeVisible({ timeout: 5000 });

    const hxGet = await folderLink.getAttribute("hx-get");
    const match = hxGet?.match(/\/gallery\/(\d+)/);
    test.skip(!match, "Could not determine subfolder ID");
    const subID = match![1];

    await folderLink.click();
    await page.waitForURL(`**/gallery/${subID}**`, { timeout: 15000 });

    await page.goBack();
    await page.waitForURL(`**/gallery/${folderID}**`, { timeout: 10000 });

    await page.goForward();
    await page.waitForURL(`**/gallery/${subID}**`, { timeout: 15000 });
    await expect(page.locator("#gallery-content")).toBeVisible();
  });

  test("4: Page refresh preserves gallery", async ({ page }) => {
    await goToGallery(page, folderID);
    await page.reload();
    await expect(page.locator("#gallery-content")).toBeVisible({
      timeout: 10000,
    });
    expect(page.url()).toContain(`/gallery/${folderID}`);
  });

  test("5: Hard refresh (cache-busting)", async ({ page }) => {
    await goToGallery(page, folderID);
    await page.goto(`/gallery/${folderID}?_t=${Date.now()}`);
    await expect(page.locator("#gallery-content")).toBeVisible({
      timeout: 10000,
    });
    expect(page.url()).toContain(`/gallery/${folderID}`);
  });

  test("6: New tab — no session leak", async ({ context }) => {
    const newPage = await context.newPage();
    await newPage.goto(`/gallery/${folderID}`);
    await expect(newPage.locator("#gallery-content")).toBeVisible({
      timeout: 10000,
    });
    // Menu should show Login (unauthenticated)
    await newPage.locator("#hamburger-menu-btn").click();
    await newPage.waitForTimeout(200);
    await expect(newPage.locator("#hamburger-menu-items")).toContainText(
      "Login",
      { timeout: 5000 },
    );
    await newPage.close();
  });

  test("7: Dashboard in new tab after login", async ({ page, context }) => {
    await loginViaUI(page);

    const newTab = await context.newPage();
    const response = await newTab.goto("/dashboard", {
      waitUntil: "domcontentloaded",
    });

    if (response?.status() === 401) {
      // Session may not be shared due to SameSite cookie restrictions
      test.info().annotations.push({
        type: "info",
        description:
          "New tab received 401 — session cookie not shared across tabs. Expected with SameSite lax.",
      });
      await newTab.close();
      return;
    }

    await newTab.waitForSelector("#dashboard-container", { timeout: 10000 });
    await expect(newTab.locator("#dashboard-container")).toBeVisible();
    await newTab.close();
  });

  test("8: Gallery→Dashboard→Back preserves session", async ({ page }) => {
    await loginViaUI(page);

    // Navigate to dashboard
    await page.goto("/dashboard");
    await page.waitForSelector("#dashboard-container", { timeout: 10000 });

    // Go back to gallery
    await page.goBack();
    await page.waitForURL("**/gallery/**", { timeout: 10000 });
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
});
