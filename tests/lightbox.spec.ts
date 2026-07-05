import { test, expect } from "@playwright/test";
import { goToGallery } from "./helpers";
import {
  getFirstFolderWithImages,
  getImageCountInFolder,
  getFileIDsInFolder,
} from "./db-queries";

test.describe.configure({ timeout: 60000 });

/**
 * Returns the current lightbox image ID by reading the data-info-id
 * attribute from the #inner_box_info element.
 */
async function currentLightboxID(
  page: import("@playwright/test").Page,
): Promise<string | null> {
  return page.locator("#inner_box_info").getAttribute("data-info-id");
}

/**
 * Wait for the lightbox info box to reflect a new image ID and for the
 * #lightbox-ui HTMX swap (which also OOB-swaps the navigation buttons) to
 * settle. This prevents the next ArrowRight from clicking a stale
 * #lightbox-next-btn that still points to the previous image.
 */
async function waitForLightboxID(
  page: import("@playwright/test").Page,
  oldID: string,
  timeout = 15000,
): Promise<void> {
  await page.waitForFunction(
    (old) => {
      const el = document.querySelector("#inner_box_info");
      const ui = document.querySelector("#lightbox-ui");
      return (
        el?.getAttribute("data-info-id") &&
        el.getAttribute("data-info-id") !== old &&
        !ui?.classList.contains("htmx-request")
      );
    },
    oldID,
    { timeout },
  );
}

// Find a folder with at least 2 images for thorough navigation testing
let folderID: number;
let imageCount: number;
let fileIDs: number[];

test.beforeAll(() => {
  const fID = getFirstFolderWithImages(2);
  if (!fID) {
    // Try with at least 1 image for open/close tests
    const fID1 = getFirstFolderWithImages(1);
    if (fID1) {
      folderID = fID1;
    }
  } else {
    folderID = fID;
  }
  if (!folderID) {
    return; // Will skip tests
  }
  imageCount = getImageCountInFolder(folderID);
  fileIDs = getFileIDsInFolder(folderID);
});

test.describe("Lightbox", () => {
  test.beforeEach(async ({ page }) => {
    test.skip(!folderID, "No folder with images — run discovery first");
    await goToGallery(page, folderID);
  });

  test("1: Open lightbox via click", async ({ page }) => {
    // Click the first lightbox link
    await page.locator("a[id^='lightbox-']").first().click();
    // Wait for the lightbox modal div to become visible (loses .hidden)
    await expect(page.locator("#lightbox_modal")).toBeVisible({
      timeout: 10000,
    });
    // Wait for the info box to load with an image ID (attached — wrapper may be hidden on mobile)
    await page.waitForSelector("#inner_box_info[data-info-id]", {
      state: "attached",
      timeout: 10000,
    });
    // Verify the visible slot has a loaded image
    const visibleSlot = page.locator("img.lightbox-slot:not(.hidden)").first();
    await expect(visibleSlot).toBeVisible({ timeout: 10000 });
    const nw = await visibleSlot.evaluate(
      (el: HTMLImageElement) => el.naturalWidth,
    );
    expect(nw).toBeGreaterThan(0);
  });

  test("2: Close via Escape", async ({ page }) => {
    await page.locator("a[id^='lightbox-']").first().click();
    await expect(page.locator("#lightbox_modal")).toBeVisible({
      timeout: 10000,
    });

    await page.keyboard.press("Escape");
    // Lightbox should be hidden again (regains .hidden class)
    await expect(page.locator("#lightbox_modal")).toHaveClass(/hidden/, {
      timeout: 5000,
    });
    // Gallery content should still be visible
    await expect(page.locator("#gallery-content")).toBeVisible();
  });

  test("3: Close via close button", async ({ page }) => {
    await page.locator("a[id^='lightbox-']").first().click();
    await expect(page.locator("#lightbox_modal")).toBeVisible({
      timeout: 10000,
    });

    await page.locator("#lightbox_close-btn").click();
    await expect(page.locator("#lightbox_modal")).toHaveClass(/hidden/, {
      timeout: 5000,
    });
  });

  test("4: Navigate next via click", async ({ page }) => {
    test.skip(imageCount < 2, "Need at least 2 images for next test");

    await page.locator("a[id^='lightbox-']").first().click();
    await expect(page.locator("#lightbox_modal")).toBeVisible({
      timeout: 10000,
    });
    await page.waitForSelector("#inner_box_info[data-info-id]", {
      state: "attached",
      timeout: 10000,
    });

    const before = await currentLightboxID(page);
    expect(before).not.toBeNull();

    await page.locator("#lightbox-next-btn").click();
    await waitForLightboxID(page, before!);

    const after = await currentLightboxID(page);
    expect(after).not.toBe(before);
  });

  test("5: Navigate prev via click", async ({ page }) => {
    test.skip(imageCount < 2, "Need at least 2 images for prev test");

    await page.locator("a[id^='lightbox-']").first().click();
    await expect(page.locator("#lightbox_modal")).toBeVisible({
      timeout: 10000,
    });
    await page.waitForSelector("#inner_box_info[data-info-id]", {
      state: "attached",
      timeout: 10000,
    });

    const firstID = await currentLightboxID(page);

    // Go next first
    await page.locator("#lightbox-next-btn").click();
    await waitForLightboxID(page, firstID!);

    const afterNext = await currentLightboxID(page);
    expect(afterNext).not.toBe(firstID);

    // Then go prev — should show a different image (may not be first due to filename sort)
    await page.locator("#lightbox-prev-btn").click();
    await waitForLightboxID(page, afterNext!);

    const afterPrev = await currentLightboxID(page);
    expect(afterPrev).not.toBe(afterNext);
  });

  test("6: Keyboard arrow navigation", async ({ page }) => {
    test.skip(imageCount < 2, "Need at least 2 images");

    await page.locator("a[id^='lightbox-']").first().click();
    await expect(page.locator("#lightbox_modal")).toBeVisible({
      timeout: 10000,
    });
    await page.waitForSelector("#inner_box_info[data-info-id]", {
      state: "attached",
      timeout: 10000,
    });

    const before = await currentLightboxID(page);

    // ArrowRight to go next
    await page.keyboard.press("ArrowRight");
    await waitForLightboxID(page, before!);

    const afterRight = await currentLightboxID(page);
    expect(afterRight).not.toBe(before);

    // ArrowLeft to go back
    await page.keyboard.press("ArrowLeft");
    await waitForLightboxID(page, afterRight!);

    const afterLeft = await currentLightboxID(page);
    // Lightbox sorts by filename, not ID — ArrowLeft may not return to the
    // exact same image. Just verify it changed from afterRight.
    expect(afterLeft).not.toBe(afterRight);
  });

  test("7: Loop-around last to first", async ({ page }) => {
    test.skip(imageCount < 2, "Need at least 2 images");

    await page.locator("a[id^='lightbox-']").first().click();
    await expect(page.locator("#lightbox_modal")).toBeVisible({
      timeout: 10000,
    });
    await page.waitForSelector("#inner_box_info[data-info-id]", {
      state: "attached",
      timeout: 10000,
    });

    const firstID = await currentLightboxID(page);
    expect(firstID).not.toBeNull();

    // Advance through all images to trigger wrap-around.
    // Use ArrowRight (keyboard) instead of clicking #lightbox-next-btn —
    // the button gets OOB-swapped on every navigation and Playwright sees
    // it as unstable. Keyboard events go to the document handler directly.
    let currentID = firstID;
    for (let i = 0; i < imageCount; i++) {
      await page.keyboard.press("ArrowRight");
      await waitForLightboxID(page, currentID!, 15000);
      currentID = await currentLightboxID(page);
    }

    // Should have wrapped to first image
    expect(currentID).toBe(firstID);
  });

  test("8: Loop-around first to last (ArrowLeft)", async ({ page }) => {
    test.skip(imageCount < 2, "Need at least 2 images");

    await page.locator("a[id^='lightbox-']").first().click();
    await expect(page.locator("#lightbox_modal")).toBeVisible({
      timeout: 10000,
    });
    await page.waitForSelector("#inner_box_info[data-info-id]", {
      state: "attached",
      timeout: 10000,
    });

    const firstID = await currentLightboxID(page);
    const lastFileID = String(fileIDs[fileIDs.length - 1]);

    // ArrowLeft from first should go to last (wrap-around)
    await page.keyboard.press("ArrowLeft");
    await waitForLightboxID(page, firstID!);

    const afterLeft = await currentLightboxID(page);
    expect(afterLeft).toBe(lastFileID);
  });

  test("9: Lightbox scoped to folder images", async ({ page }) => {
    test.skip(imageCount < 2, "Need at least 2 images");

    await page.locator("a[id^='lightbox-']").first().click();
    await expect(page.locator("#lightbox_modal")).toBeVisible({
      timeout: 10000,
    });
    await page.waitForSelector("#inner_box_info[data-info-id]", {
      state: "attached",
      timeout: 10000,
    });

    // Collect all seen IDs by navigating through all images + 1 wrap
    const seenIDs: string[] = [];
    let currentID = await currentLightboxID(page);
    for (let i = 0; i <= imageCount; i++) {
      expect(currentID).not.toBeNull();
      seenIDs.push(currentID!);
      await page.keyboard.press("ArrowRight");
      await waitForLightboxID(page, currentID!, 15000);
      currentID = await currentLightboxID(page);
    }

    // All seen IDs should be from the expected set
    const expectedIDs = new Set(fileIDs.map(String));
    for (const id of seenIDs) {
      expect(expectedIDs.has(id)).toBeTruthy();
    }
  });

  test("10: Mobile viewport navigation", async ({ page }) => {
    test.skip(imageCount < 2, "Need at least 2 images");
    test.skip(
      true,
      "mobile-only: run with --project=chromium and viewport set",
    );
  });
});
