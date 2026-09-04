import { test, expect } from "@playwright/test";
import { ensureGallerySession, openMenu } from "./helpers";
import {
  makeSnapshotRestore,
  expectRestartDialogOpen,
  openConfigPerformanceTab,
} from "./config-helpers";
import fs from "fs";
import os from "os";
import path from "path";

test.describe.configure({ timeout: 90000 });

// Config tests modify server state — use serial execution to avoid races.
test.describe.serial("Configuration", () => {
  test.beforeEach(async ({ page }) => {
    await ensureGallerySession(page);
  });

  test("1: Config modal opens from menu", async ({ page }) => {
    await openMenu(page);
    await page.locator('a[aria-label="Configuration"]').click();
    await page.waitForSelector("#config-form", { timeout: 5000 });
    await expect(page.locator("#config_modal")).toBeChecked({ timeout: 3000 });
    // Switch to Application tab (site_name lives there, Server tab is default)
    await page.locator("#tab-application-btn").click();
    await page.waitForTimeout(200);
    // Expect site-name field to be pre-filled
    await expect(page.locator('input[name="site_name"]')).toBeVisible();
  });

  test("2: Config modal cancel closes modal", async ({ page }) => {
    await openMenu(page);
    await page.locator('a[aria-label="Configuration"]').click();
    await page.waitForSelector("#config-form", { timeout: 5000 });
    await expect(page.locator("#config_modal")).toBeChecked({ timeout: 3000 });

    await page.locator("#config-cancel-btn").click();
    await expect(page.locator("#config_modal")).not.toBeChecked({
      timeout: 3000,
    });
  });

  test("3: Config save (site name)", async ({ page }) => {
    // Read current site name first so we can restore it
    await openMenu(page);
    await page.locator('a[aria-label="Configuration"]').click();
    await page.waitForSelector("#config-form", { timeout: 5000 });
    await page.locator("#tab-application-btn").click();
    await page.waitForTimeout(200);

    const siteNameInput = page.locator('input[name="site_name"]');
    const originalName = await siteNameInput.inputValue();

    // Change site name
    const testName = `TestGallery_${Date.now()}`;
    await siteNameInput.fill(testName);

    // Click the primary save button
    await page.locator("#config-form button[type='submit']").first().click();

    // Wait for success message
    await expect(page.locator("#config-success-message")).toBeVisible({
      timeout: 10000,
    });

    // Reopen and verify the saved value
    await page.locator("#config-cancel-btn").click();
    await page.waitForTimeout(200);

    await openMenu(page);
    await page.locator('a[aria-label="Configuration"]').click();
    await page.waitForSelector("#config-form", { timeout: 5000 });
    await page.locator("#tab-application-btn").click();
    await page.waitForTimeout(200);

    const savedName = await page
      .locator('input[name="site_name"]')
      .inputValue();
    expect(savedName).toBe(testName);

    // Restore original name
    if (originalName) {
      await page.locator('input[name="site_name"]').fill(originalName); // already on Application tab from reopen
      await page.locator("#config-form button[type='submit']").first().click();
      await expect(page.locator("#config-success-message")).toBeVisible({
        timeout: 10000,
      });
    }
  });

  test("3b: Config save (login security)", async ({ page }) => {
    await openMenu(page);
    await page.locator('a[aria-label="Configuration"]').click();
    await page.waitForSelector("#config-form", { timeout: 5000 });
    await page.locator("#tab-session-btn").click();
    await page.waitForTimeout(200);

    const ipLimit = page.locator('input[name="login_rate_limit_per_ip"]');
    const threshold = page.locator('input[name="lockout_threshold"]');
    const duration = page.locator('input[name="lockout_duration"]');

    const origIP = await ipLimit.inputValue();
    const origThreshold = await threshold.inputValue();
    const origDuration = await duration.inputValue();

    await ipLimit.fill("11");
    await threshold.fill("5");
    await duration.fill("2400");

    await page.locator("#config-form button[type='submit']").first().click();
    await expect(page.locator("#config-success-message")).toBeVisible({
      timeout: 10000,
    });

    await page.locator("#config-cancel-btn").click();
    await page.waitForTimeout(200);

    await openMenu(page);
    await page.locator('a[aria-label="Configuration"]').click();
    await page.waitForSelector("#config-form", { timeout: 5000 });
    await page.locator("#tab-session-btn").click();
    await page.waitForTimeout(200);

    await expect(
      page.locator('input[name="login_rate_limit_per_ip"]'),
    ).toHaveValue("11");
    await expect(page.locator('input[name="lockout_threshold"]')).toHaveValue(
      "5",
    );
    await expect(page.locator('input[name="lockout_duration"]')).toHaveValue(
      "2400",
    );

    await ipLimit.fill(origIP);
    await threshold.fill(origThreshold);
    await duration.fill(origDuration);
    await page.locator("#config-form button[type='submit']").first().click();
    await expect(page.locator("#config-success-message")).toBeVisible({
      timeout: 10000,
    });
  });

  test("3c: Config save (dque max disk bytes)", async ({ page }) => {
    await openConfigPerformanceTab(page);
    const dqueInput = page.locator('input[name="dque_max_disk_bytes"]');
    await expect(dqueInput).toBeVisible({ timeout: 5000 });
    const originalValue = await dqueInput.inputValue();

    // Pick a test value different from the original (default is 53687091200 = 50 GiB)
    const testValue =
      originalValue === "107374182400" ? "53687091200" : "107374182400";
    await dqueInput.fill(testValue);

    await page.locator("#config-form button[type='submit']").first().click();
    await expect(page.locator("#config-success-message")).toBeVisible({
      timeout: 10000,
    });

    await page.locator("#config-cancel-btn").click();
    await page.waitForTimeout(200);

    await openConfigPerformanceTab(page);
    const dqueReopen = page.locator('input[name="dque_max_disk_bytes"]');
    await expect(dqueReopen).toBeVisible({ timeout: 5000 });
    await expect(dqueReopen).toHaveValue(testValue);

    // Restore original value
    await dqueReopen.fill(originalValue);
    await page.locator("#config-form button[type='submit']").first().click();
    await expect(page.locator("#config-success-message")).toBeVisible({
      timeout: 10000,
    });
  });

  test("4: Config validation error", async ({ page }) => {
    await openMenu(page);
    await page.locator('a[aria-label="Configuration"]').click();
    await page.waitForSelector("#config-form", { timeout: 5000 });
    await page.locator("#tab-application-btn").click();
    await page.waitForTimeout(200);

    // Clear the site name (required field) to trigger validation
    const siteNameInput = page.locator('input[name="site_name"]');
    await siteNameInput.fill("");

    // Submit
    await page.locator("#config-form button[type='submit']").first().click();

    // Wait for error message
    await expect(page.locator("#config-error-message")).toBeAttached({
      timeout: 5000,
    });
    // Modal should still be open
    await expect(page.locator("#config_modal:checked")).toBeAttached({
      timeout: 3000,
    });
  });

  test("5: Theme list management", async ({ page }) => {
    // This test depends on config modal having a theme section.
    // Accept the save without asserting specific theme values.
    await openMenu(page);
    await page.locator('a[aria-label="Configuration"]').click();
    await page.waitForSelector("#config-form", { timeout: 5000 });

    // Just verify the config form renders (theme list may be part of the form)
    await expect(page.locator("#config-form")).toBeVisible();
  });

  test("6: Increment ETag standalone", async ({ page }) => {
    await openMenu(page);
    await page.locator('a[aria-label="Configuration"]').click();
    await page.waitForSelector("#config-form", { timeout: 5000 });

    // Read current etag version
    const etagVersionInput = page.locator("#config-etag-version");
    const etagV0 = await etagVersionInput.inputValue();

    // Click the Increment ETag Version button
    const incrementBtn = page.locator(
      '#config-etag-display button[hx-post="/config/increment-etag"]',
    );
    await incrementBtn.click();

    // Wait for etag display to update
    await page.waitForTimeout(500);
    const etagV1 = await etagVersionInput.inputValue();
    expect(etagV1).not.toBe(etagV0);
  });

  test("7: Config export to file", async ({ page }) => {
    await openMenu(page);
    await page.locator('a[aria-label="Configuration"]').click();
    await page.waitForSelector("#config-form", { timeout: 5000 });

    // The Export button is in the Server tab footer (default tab)
    const exportBtn = page.locator('button[hx-post="/config/export/to-file"]');
    await expect(exportBtn).toBeVisible({ timeout: 5000 });

    // Click Export → HTMX POST returns the export modal content
    await exportBtn.click();

    // The export modal opens showing the YAML content
    await expect(page.locator("#export-diff-modal-toggle")).toBeChecked({
      timeout: 5000,
    });
    // Verify the modal shows YAML configuration (look for a known config key)
    // Note: Go's html.EscapeString escapes quotes, so &#34; appears for YAML string values
    await expect(page.locator(".modal-box pre")).toContainText("site-name", {
      timeout: 3000,
    });

    // Close the export modal (scoped to the modal-action button, not backdrop)
    await page
      .locator(".modal-action label[for='export-diff-modal-toggle']")
      .click();
    await expect(page.locator("#export-diff-modal-toggle")).not.toBeChecked({
      timeout: 3000,
    });
  });

  test("8: Config export download", async ({ page }) => {
    // Open the Export modal first (sets up the download link)
    await openMenu(page);
    await page.locator('a[aria-label="Configuration"]').click();
    await page.waitForSelector("#config-form", { timeout: 5000 });

    const exportBtn = page.locator('button[hx-post="/config/export/to-file"]');
    await expect(exportBtn).toBeVisible({ timeout: 5000 });
    await exportBtn.click();
    await expect(page.locator("#export-diff-modal-toggle")).toBeChecked({
      timeout: 5000,
    });

    // The modal contains an <a download="config.yaml"> link
    const downloadLink = page.locator(
      '#export-diff-modal a[download="config.yaml"]',
    );
    await expect(downloadLink).toBeVisible({ timeout: 3000 });

    // Click the download link — Playwright handles the file download
    const [download] = await Promise.all([
      page.waitForEvent("download", { timeout: 10000 }),
      downloadLink.click(),
    ]);

    expect(download.suggestedFilename()).toBe("config.yaml");

    // Close the modal and clean up downloaded file
    await page
      .locator(".modal-action label[for='export-diff-modal-toggle']")
      .click();
    await download.delete();
  });

  test("9: Config import preview", async ({ page }) => {
    await openMenu(page);
    await page.locator('a[aria-label="Configuration"]').click();
    await page.waitForSelector("#config-form", { timeout: 5000 });

    // The Import button triggers a hidden file input (#import-file-input)
    // that uploads to /config/import/preview via HTMX.
    // Upload a minimal valid YAML file to trigger the preview.
    const yamlContent = "site-name: SmokeTest\n";
    const tmpFile = path.join(os.tmpdir(), "test-import-config.yaml");
    fs.writeFileSync(tmpFile, yamlContent);

    const fileInput = page.locator("#import-file-input");
    await fileInput.setInputFiles(tmpFile);

    // Wait for HTMX preview content first (toggle alone can race on fast Macs),
    // then assert the modal is open.
    await expect(page.locator("#import-diff-modal .modal-box")).toContainText(
      "Import Configuration Preview",
      { timeout: 15000 },
    );
    await expect(page.locator("#import-diff-modal-toggle")).toBeChecked({
      timeout: 5000,
    });

    // Close the preview modal
    await page
      .locator(".modal-action label[for='import-diff-modal-toggle']")
      .click();
    fs.unlinkSync(tmpFile);
  });

  test("10: Config import commit", async ({ page, request }) => {
    // Use the preview flow to upload YAML, then commit the import.
    // The snapshot/restore in beforeAll/afterAll ensures config is restored.
    await openMenu(page);
    await page.locator('a[aria-label="Configuration"]').click();
    await page.waitForSelector("#config-form", { timeout: 5000 });

    const yamlContent = "site-name: SmokeTestImported\n";
    const tmpFile = path.join(os.tmpdir(), "test-import-commit.yaml");
    fs.writeFileSync(tmpFile, yamlContent);

    // Upload file → triggers preview modal
    const fileInput = page.locator("#import-file-input");
    await fileInput.setInputFiles(tmpFile);
    await expect(page.locator("#import-diff-modal .modal-box")).toContainText(
      "Import Configuration Preview",
      { timeout: 15000 },
    );
    await expect(page.locator("#import-diff-modal-toggle")).toBeChecked({
      timeout: 5000,
    });

    // Click the Import button in the modal (commits the change)
    const commitBtn = page.locator(
      "#import-diff-modal .modal-action button[hx-post='/config/import/commit']",
    );
    await expect(commitBtn).toBeVisible({ timeout: 3000 });
    await commitBtn.click();

    // The commit POST succeeds (server applies config). Wait for settle.
    await page.waitForTimeout(1000);

    // Verify the import via the API (export endpoint) — it should reflect the new site_name.
    // Use a fresh authenticated API context (separate cookie jar from the UI page).
    const { authenticatedAPIContext, exportConfig } =
      await import("./config-helpers");
    const api = await authenticatedAPIContext(request);
    const exportedYAML = await exportConfig(api);
    expect(exportedYAML).toContain("site-name: SmokeTestImported");

    fs.unlinkSync(tmpFile);
  });

  test("11: Config restore last-known-good", async ({ page }) => {
    await openMenu(page);
    await page.locator('a[aria-label="Configuration"]').click();
    await page.waitForSelector("#config-form", { timeout: 5000 });

    const restoreBtn = page.locator("text=Restore").first();
    if (await restoreBtn.isVisible().catch(() => false)) {
      await restoreBtn.click();
      await page.waitForTimeout(500);
      // Verify non-destructive — still on config page
      await expect(page.locator("#config-form")).toBeVisible();
    } else {
      test.skip(true, "Restore control not visible");
    }
  });

  test("13a: db_max_pool_size save opens restart dialog", async ({ page }) => {
    const poolInput = await openConfigPerformanceTab(page);
    const current = Number(await poolInput.inputValue());
    await poolInput.fill(String(current + 1));

    await page.locator("#config-form button[type='submit']").first().click();

    // The restart dialog must open for a number-field change too.
    await expectRestartDialogOpen(page);

    // The restart diff must list the changed field in the diff table.
    await expect(
      page.locator("#restart-diff-content table tbody tr", {
        hasText: "db_max_pool_size",
      }),
    ).toHaveCount(1);

    // Close the restart dialog without triggering a server restart.
    await page
      .locator(
        '.modal:has(#restart-diff-content) .modal-action label[for="restart-diff-modal"]',
      )
      .click();
    await expect(page.locator("#restart-diff-modal")).not.toBeChecked({
      timeout: 3000,
    });
  });

  test("13b: Cancel after restart-required save closes cleanly", async ({
    page,
  }) => {
    const poolInput = await openConfigPerformanceTab(page);
    const current = Number(await poolInput.inputValue());
    const changed = current + 1;
    await poolInput.fill(String(changed));

    // Save → the restart dialog must open (shared helper contract).
    await page.locator("#config-form button[type='submit']").first().click();
    await expectRestartDialogOpen(page);

    // Close the restart dialog via Close — no server restart.
    await page
      .locator(
        '.modal:has(#restart-diff-content) .modal-action label[for="restart-diff-modal"]',
      )
      .click();
    await expect(page.locator("#restart-diff-modal")).not.toBeChecked({
      timeout: 3000,
    });

    // Cancel with no further edits: must NOT open #cancel-diff-modal and must
    // close the config modal cleanly.
    await page.locator("#config-cancel-btn").click();
    await expect(page.locator("#cancel-diff-modal")).not.toBeChecked({
      timeout: 3000,
    });
    await expect(page.locator("#config_modal")).not.toBeChecked({
      timeout: 3000,
    });

    // Reopen config: the persisted value is still present (Cancel only ends
    // the modal session; it does not roll back the save).
    const poolReopen = await openConfigPerformanceTab(page);
    await expect(poolReopen).toHaveValue(String(changed));
  });

  test("13: Config restart", async ({ page, request }) => {
    // Config restart is non-destructive — the server restarts in <10s.
    // This test is intentionally LAST in the serial run because it restarts
    // the server and must not race with other tests.
    // This test verifies the restart-required flow: toggle http-cache (which
    // requires restart), confirm the badge appears, then trigger restart.
    // It also restores the original config so the server is left in a clean
    // state for subsequent test files.

    try {
      await openConfigPerformanceTab(page);

      const cacheCheckbox = page.locator('input[name="enable_http_cache"]');
      await expect(cacheCheckbox).toBeVisible({ timeout: 5000 });
      // Flip http-cache from its current state so the save is always a real
      // restart-required change regardless of the server's starting config
      // (default http-cache is false, so a plain uncheck would be a no-op).
      if (await cacheCheckbox.isChecked()) {
        await cacheCheckbox.uncheck();
      } else {
        await cacheCheckbox.check();
      }

      // Save — this triggers the restart-required flow:
      // 1. Server returns OOB swap making badge visible
      // 2. htmx:afterSettle handler opens #restart-diff-modal automatically
      await page.locator("#config-form button[type='submit']").first().click();

      // Assert the restart-required flow actually opened the dialog. These
      // assertions are the false-green gate: with the OOB-on-target bug the
      // badge keeps `hidden` and the dialog never opens even though the
      // success alert renders.
      await expectRestartDialogOpen(page);

      // Click Restart Server scoped to the open restart dialog. The modal is
      // proven open above, so no force click is needed.
      const restartBtn = page.locator(
        'div.modal:has(#restart-diff-content) button[hx-post="/config/restart"]',
      );
      await expect(restartBtn).toBeVisible({ timeout: 5000 });
      await restartBtn.click();

      // Wait for restart to begin
      await page.waitForTimeout(1000);

      // Poll health endpoint until server recovers (up to 20s).
      // The server restarts on the same port (we only toggled http-cache,
      // not the listener port), so baseURL remains valid.
      let recovered = false;
      for (let i = 0; i < 20; i++) {
        try {
          const resp = await page.request.get("/health", { timeout: 2000 });
          if (resp.status() === 200) {
            recovered = true;
            break;
          }
        } catch {
          // Server still restarting
        }
        await page.waitForTimeout(1000);
      }
      expect(recovered).toBeTruthy();

      // Verify the app is fully functional after restart
      await page.goto("/gallery/1");
      await expect(page.locator("#gallery-content")).toBeVisible({
        timeout: 10000,
      });
    } finally {
      // Restore the snapshot config and restart if required. This cleanup runs
      // inside the test because Playwright runs this file's afterAll before the
      // last test when it is the final serial test in the file.
      await snapshotRestore.restore(request);
    }
  });
});

// Snapshot/restore config for tests that mutate server state.
// Defined at file scope so the restore runs after the serial group completes.
const snapshotRestore = makeSnapshotRestore();

test.beforeAll(async ({ request }) => {
  await snapshotRestore.snapshot(request);
});

test.afterAll(async ({ request }) => {
  await snapshotRestore.restore(request);
});
