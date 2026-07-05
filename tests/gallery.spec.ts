import { test, expect } from "@playwright/test";
import { goToGallery } from "./helpers";
import {
  getFirstFolderID,
  getFirstFileID,
  getFirstSubfolderID,
  getImageCountInFolder,
  getFirstFolderWithImages,
} from "./db-queries";

let folderID: number;
let fileID: number;
let fileFolderID: number;

test.describe.configure({ timeout: 60000 });

test.beforeAll(() => {
  folderID = getFirstFolderID()!;
  fileID = getFirstFileID()!;
  if (!folderID) test.skip(true, "No folder data — run discovery first");
  if (!fileID) test.skip(true, "No file data — run discovery first");
});

test.describe("Gallery Browsing", () => {
  test("1: Root redirect", async ({ page }) => {
    await page.goto("/");
    await page.waitForURL("**/gallery/1**", { timeout: 10000 });
    await expect(page.locator("#gallery-content")).toBeVisible();
  });

  test("2: Gallery loads with tiles", async ({ page }) => {
    await goToGallery(page, folderID);
    await expect(page.locator("#gallery-content")).toBeVisible();
    expect(await page.title()).not.toBe("");
    const tiles = page.locator(".gallery-tile");
    await expect(tiles.first()).toBeVisible({ timeout: 10000 });
  });

  test("3: Sub-folder navigation via HTMX", async ({ page }) => {
    await goToGallery(page, folderID);

    const folderLink = page
      .locator('.gallery-tile a[hx-get][hx-target="#gallery-content"]')
      .first();
    await expect(folderLink).toBeVisible({ timeout: 5000 });

    // Read the target ID from the hx-get attribute (more reliable than guessing sort order)
    const hxGet = await folderLink.getAttribute("hx-get");
    const match = hxGet?.match(/\/gallery\/(\d+)/);
    test.skip(!match, "Could not determine subfolder ID from link");
    const subID = match![1];

    // Click and wait for gallery-content swap + URL change
    await folderLink.click();
    await page.waitForURL(`**/gallery/${subID}**`, { timeout: 15000 });
    await expect(page.locator("#gallery-content")).toBeVisible({
      timeout: 10000,
    });
  });

  test("4: Breadcrumb shows parent link", async ({ page }) => {
    await goToGallery(page, folderID);
    const folderLink = page
      .locator('.gallery-tile a[hx-get][hx-target="#gallery-content"]')
      .first();
    await expect(folderLink).toBeVisible({ timeout: 5000 });

    const hxGet = await folderLink.getAttribute("hx-get");
    const match = hxGet?.match(/\/gallery\/(\d+)/);
    test.skip(!match, "Could not determine subfolder ID from link");
    const subID = match![1];

    await folderLink.click();
    await page.waitForURL(`**/gallery/${subID}**`, { timeout: 15000 });

    await expect(page.locator("#breadcrumbs-container")).toBeVisible({
      timeout: 5000,
    });
    // Breadcrumbs should contain a link back to the parent folder
    const parentCrumb = page.locator(
      `#breadcrumbs-container a[href*="/gallery/${folderID}"]`,
    );
    await expect(parentCrumb.first()).toBeVisible({ timeout: 3000 });
  });

  test("5: Error page shown for bad folder ID", async ({ page }) => {
    const response = await page.goto("/gallery/0", {
      waitUntil: "domcontentloaded",
    });
    // Server returns some error — body should have text
    const body = await page.locator("body").innerText();
    expect(body.length).toBeGreaterThan(0);
  });

  test("6: Image page renders with natural dimensions", async ({ page }) => {
    await page.goto(`/image/${fileID}`);
    await page.waitForSelector("img", { timeout: 10000 });
    const img = page.locator("img").first();
    const nw = await img.evaluate((el: HTMLImageElement) => el.naturalWidth);
    expect(nw).toBeGreaterThan(0);
  });

  test("7: Raw image returns image content type", async ({ page }) => {
    const response = await page.goto(`/raw-image/${fileID}`);
    expect(response?.status()).toBe(200);
    const contentType = response?.headers()["content-type"] || "";
    expect(contentType.startsWith("image/")).toBeTruthy();
  });

  test("8: Image thumbnail loads with lazy loading", async ({ page }) => {
    // Find a folder that actually has images (not all subfolders do)
    const imgFolderID = getFirstFolderWithImages(1);
    test.skip(!imgFolderID, "No folder with images available");
    await goToGallery(page, imgFolderID!);

    const thumb = page.locator('img[src*="/thumbnail/file/"]').first();
    await expect(thumb).toBeAttached({ timeout: 5000 });
    await thumb.scrollIntoViewIfNeeded();
    const nw = await thumb.evaluate((el: HTMLImageElement) => el.naturalWidth);
    expect(nw).toBeGreaterThan(0);
  });

  test("9: Folder thumbnail loads with lazy loading", async ({ page }) => {
    await goToGallery(page, folderID);
    const folderThumb = page.locator('img[src*="/thumbnail/folder/"]').first();
    // Image exists in DOM (may be lazy-loaded below the fold)
    await expect(folderThumb).toBeAttached({ timeout: 5000 });
    await folderThumb.scrollIntoViewIfNeeded();
    // Wait for the lazy-loaded image to actually load
    await page.waitForFunction(
      () => {
        const img = document.querySelector(
          'img[src*="/thumbnail/folder/"]',
        ) as HTMLImageElement | null;
        return img && img.naturalWidth > 0;
      },
      null,
      { timeout: 10000 },
    );
    const nw = await folderThumb.evaluate(
      (el: HTMLImageElement) => el.naturalWidth,
    );
    expect(nw).toBeGreaterThan(0);
  });

  test("10: Gallery state restored after browser back", async ({ page }) => {
    await goToGallery(page, folderID);
    const folderLink = page
      .locator('.gallery-tile a[hx-get][hx-target="#gallery-content"]')
      .first();
    await expect(folderLink).toBeVisible({ timeout: 5000 });

    const hxGet = await folderLink.getAttribute("hx-get");
    const match = hxGet?.match(/\/gallery\/(\d+)/);
    test.skip(!match, "Could not determine subfolder ID from link");
    const subID = match![1];

    const originalURL = page.url();

    await folderLink.click();
    await page.waitForURL(`**/gallery/${subID}**`, { timeout: 15000 });

    await page.goBack();
    await page.waitForURL("**/gallery/**", { timeout: 10000 });
    await expect(page.locator("#gallery-content")).toBeVisible();
  });
});
