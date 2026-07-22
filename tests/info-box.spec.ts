import { test, expect, type Page } from "@playwright/test";
import { goToGallery } from "./helpers";
import {
  getFirstFolderID,
  getFirstFileID,
  getFirstSubfolderID,
  getFolderIDForFile,
  getImageCountInFolder,
  getFileName,
  getFolderName,
} from "./db-queries";

test.describe.configure({ timeout: 60000 });

let folderID: number;
let fileID: number;
let fileFolderID: number;
let subfolderID: number; // A folder tile that actually appears on gallery/1
test.beforeAll(() => {
  folderID = getFirstFolderID()!; // root folder (1)
  fileID = getFirstFileID()!;
  if (!folderID) test.skip(true, "No folder data — run discovery first");
  if (!fileID) test.skip(true, "No file data — run discovery first");
  fileFolderID = getFolderIDForFile(fileID)!;
  if (!fileFolderID)
    test.skip(
      true,
      `No folder found for file ${fileID} — db may be inconsistent`,
    );
  // Get a subfolder that appears as a tile on the root gallery page
  subfolderID = getFirstSubfolderID(folderID)!;
  if (!subfolderID)
    test.skip(true, "No subfolder under root — cannot test folder info hover");
});

/** Ensure #boxgallery has .populated so the mouseleave clear guard can run. */
async function ensureGalleryPopulated(
  page: Page,
  galleryFolderID: number,
): Promise<void> {
  await page.goto(`/gallery/${galleryFolderID}?cb=${Date.now()}`);
  await page.waitForSelector("#gallery-content", { timeout: 10000 });
  await page.locator("#boxgallery").evaluate((el) => {
    el.classList.add("populated");
  });
}

async function pinInfoBox(page: Page): Promise<void> {
  const pinned = await page.evaluate(() =>
    document.body.classList.contains("info-pinned"),
  );
  if (pinned) {
    return;
  }
  await page.locator("#info-btn").click();
  await expect(page.locator("body")).toHaveClass(/info-pinned/);
  await expect(page.locator("#box_info_wrapper")).not.toHaveClass(/hidden/);
}

async function unpinInfoBox(page: Page): Promise<void> {
  const pinned = await page.evaluate(() =>
    document.body.classList.contains("info-pinned"),
  );
  if (!pinned) {
    return;
  }
  await page.locator("#info-btn").click();
  await expect(page.locator("body")).not.toHaveClass(/info-pinned/);
}

async function hoverTileForInfo(page: Page, tileID: number): Promise<void> {
  const tile = page.locator(`#gallery-tile-${tileID}`).first();
  await expect(tile).toBeVisible({ timeout: 5000 });
  await tile.scrollIntoViewIfNeeded();
  await tile.hover();
  await page.waitForSelector(`#inner_box_info[data-info-id="${tileID}"]`, {
    state: "attached",
    timeout: 10000,
  });
}

/** Move pointer off the gallery grid to fire #boxgallery mouseleave. */
async function leaveGalleryGrid(page: Page): Promise<void> {
  const gallery = page.locator("#boxgallery");
  await gallery.hover({ position: { x: 8, y: 8 } });
  const box = await gallery.boundingBox();
  if (box) {
    await page.mouse.move(box.x + box.width + 50, box.y - 30);
  } else {
    await page.locator("#hamburger-menu-btn").hover({ force: true });
  }
  await page.waitForTimeout(300);
}

test.describe("Info Box", () => {
  test("1: Folder info endpoint returns folder name and image count", async ({
    page,
  }) => {
    await goToGallery(page, folderID);
    const r = await page.request.get(`/info/folder/${folderID}`);
    expect(r.status()).toBe(200);
    const body = await r.text();

    const name = getFolderName(folderID);
    if (name) {
      expect(body).toContain(name);
    }
    const count = getImageCountInFolder(folderID);
    expect(body).toContain(String(count));
  });

  test("2: Image info endpoint returns filename and dimensions", async ({
    page,
  }) => {
    await goToGallery(page, fileFolderID);
    const r = await page.request.get(`/info/image/${fileID}`);
    expect(r.status()).toBe(200);
    const body = await r.text();

    const filename = getFileName(fileID);
    if (filename) {
      expect(body).toContain(filename);
    }
    // Should contain dimension info or file size info
    expect(body).toMatch(/x\s*\d+/);
  });

  test("3: Folder info via hover flow", async ({ page }) => {
    await goToGallery(page, folderID);

    // Ensure info box wrapper is visible
    const wrapper = page.locator("#box_info_wrapper");
    if (await wrapper.evaluate((el) => el.classList.contains("hidden"))) {
      await page.locator("#info-btn").click();
      await page.waitForTimeout(200);
    }

    // Hover over a subfolder tile that appears on the root gallery page
    const folderTile = page.locator(`#gallery-tile-${subfolderID}`).first();
    await expect(folderTile).toBeVisible({ timeout: 5000 });

    await folderTile.hover();
    await page.waitForTimeout(1500); // mouseenter delay:1000ms

    // Wait for info box to load the folder info
    await page.waitForSelector(
      `#inner_box_info[data-info-id="${subfolderID}"]`,
      {
        state: "attached",
        timeout: 10000,
      },
    );
    const name = getFolderName(subfolderID);
    if (name) {
      await expect(page.locator("#box_info")).toContainText(name, {
        timeout: 3000,
      });
    }
  });

  test("4: Image info via hover flow", async ({ page }) => {
    await goToGallery(page, fileFolderID);

    // Ensure info box wrapper is visible
    const wrapper = page.locator("#box_info_wrapper");
    if (await wrapper.evaluate((el) => el.classList.contains("hidden"))) {
      await page.locator("#info-btn").click();
      await page.waitForTimeout(200);
    }

    // Hover over the image tile (scroll into view first — it may be below the fold)
    const imgTile = page.locator(`#gallery-tile-${fileID}`).first();
    await expect(imgTile).toBeAttached({ timeout: 5000 });
    await imgTile.scrollIntoViewIfNeeded();
    await page.waitForTimeout(200);

    await imgTile.hover();
    await page.waitForTimeout(1500); // mouseenter delay:1000ms

    // Wait for info box to load image info
    await page.waitForSelector(`#inner_box_info[data-info-id="${fileID}"]`, {
      state: "attached",
      timeout: 10000,
    });
    const filename = getFileName(fileID);
    if (filename) {
      await expect(page.locator("#box_info")).toContainText(filename, {
        timeout: 3000,
      });
    }
  });

  test("5: Info box updates when hovering different tile", async ({ page }) => {
    // This test requires at least two tiles on the page
    await goToGallery(page, folderID);

    // Ensure info box wrapper is visible
    const wrapper = page.locator("#box_info_wrapper");
    if (await wrapper.evaluate((el) => el.classList.contains("hidden"))) {
      await page.locator("#info-btn").click();
      await page.waitForTimeout(200);
    }

    // Hover the first gallery tile to load an info box
    const firstTile = page.locator(".gallery-tile").first();
    await firstTile.hover();
    await page.waitForTimeout(1500);
    const firstInfoID = await page
      .locator("#inner_box_info")
      .getAttribute("data-info-id");
    expect(firstInfoID).not.toBeNull();

    // Try a different tile
    const allTiles = page.locator(".gallery-tile");
    const count = await allTiles.count();
    if (count < 2) {
      test.skip(true, "Need at least 2 tiles on this page");
      return;
    }
    const secondTile = allTiles.nth(count > 1 ? 1 : 0);
    if (secondTile) {
      await secondTile.hover();
      await page.waitForTimeout(1500);
      const secondInfoID = await page
        .locator("#inner_box_info")
        .getAttribute("data-info-id");

      if (count >= 2) {
        expect(secondInfoID).not.toBe(firstInfoID);
      }
    }
  });

  test("6: Pinned info box survives mouseleave from gallery grid", async ({
    page,
  }) => {
    await ensureGalleryPopulated(page, folderID);
    await pinInfoBox(page);
    await hoverTileForInfo(page, subfolderID);

    await leaveGalleryGrid(page);

    await expect(page.locator("#inner_box_info")).toHaveAttribute(
      "data-info-id",
      String(subfolderID),
    );
  });

  test("7: Unpinned info box clears on mouseleave from gallery grid", async ({
    page,
  }) => {
    await ensureGalleryPopulated(page, folderID);
    await pinInfoBox(page);
    await hoverTileForInfo(page, subfolderID);
    await unpinInfoBox(page);

    await leaveGalleryGrid(page);

    await expect(page.locator("#inner_box_info")).toHaveCount(0);
  });
});
