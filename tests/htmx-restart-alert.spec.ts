import { test, expect } from "@playwright/test";
import fs from "fs";
import path from "path";

/**
 * HTMX response fixture — browser-level swap contract for the config
 * restart-required save alert.
 *
 * Guards the Task 2 template fix (web/templates/config-ui/config-save-restart-alert.html.tmpl):
 * `#config-success-message` must be the MAIN swap target (no hx-swap-oob)
 * while `#config-restart-badge` is a true OOB fragment that replaces the
 * pre-save hidden badge. A regression that re-marks the success message OOB
 * (the original bug: form hx-target + OOB on the same id corrupts the badge
 * swap, so the restart dialog never opens) fails here without a full admin
 * save round-trip.
 *
 * PREREQUISITE: the dev server (`air`) must be running on http://localhost:8083
 * — HTMX is loaded from the app's own static assets (same file the gallery
 * uses), and the fixture page is served from the app origin.
 *
 * The fragment is read from the production template file on disk (no Go
 * variables in that template), so template edits are exercised directly.
 */

const RESTART_ALERT_TEMPLATE_PATH = path.join(
  __dirname,
  "../web/templates/config-ui/config-save-restart-alert.html.tmpl",
);

function loadRestartAlertFragment(): string {
  return fs.readFileSync(RESTART_ALERT_TEMPLATE_PATH, "utf8").trim();
}

/**
 * Minimal page mirroring the pre-save DOM state in
 * web/templates/config-modal.html.tmpl: an empty `#config-success-message`
 * target and a `#config-restart-badge` with class `hidden`. The trigger
 * button uses the same swap spec as `#config-form` (outerHTML into
 * `#config-success-message`).
 */
const FIXTURE_PAGE = `<!DOCTYPE html>
<html>
  <head>
    <script src="/static/js/htmx.min.js"></script>
  </head>
  <body>
    <div id="config-success-message" class="flex-shrink-0 px-6 pt-2"></div>
    <div id="config-restart-badge-host">
      <span id="config-restart-badge" class="badge badge-warning hidden">Restart Required</span>
    </div>
    <button
      id="trigger-config-save"
      type="button"
      hx-post="/__fixture__/config-save"
      hx-target="#config-success-message"
      hx-swap="outerHTML"
    >
      Save
    </button>
  </body>
</html>`;

test.describe("Config restart alert HTMX fixture", () => {
  test("restart-alert fragment contract: success not OOB, badge OOB without hidden", async ({
    page,
  }) => {
    // Load the generated fragment into a scratch container and assert its
    // structure through the DOM (not string matching) so the generator itself
    // enforces the swap contract.
    await page.setContent('<div id="scratch"></div>');
    await page.locator("#scratch").evaluate((el, html) => {
      el.innerHTML = html;
    }, loadRestartAlertFragment());

    const success = page.locator("#scratch #config-success-message");
    await expect(success).toHaveCount(1);
    // Main swap target must NOT be OOB: the form targets this id with
    // outerHTML, so OOB here corrupts the response (the original bug).
    await expect(success).not.toHaveAttribute("hx-swap-oob", /.*/);
    await expect(success).toContainText(
      "Server restart required for changes to take effect.",
    );

    const badge = page.locator("#scratch #config-restart-badge");
    await expect(badge).toHaveCount(1);
    // True OOB fragment without `hidden`, so it replaces the pre-save badge.
    await expect(badge).toHaveAttribute("hx-swap-oob", "outerHTML");
    await expect(badge).not.toHaveClass(/hidden/);
    await expect(badge).toHaveText("Restart Required");
  });

  test("restart-alert OOB swap removes hidden from badge (htmx fixture)", async ({
    page,
  }) => {
    // Serve the fixture page and the fragment from the app origin so the
    // real htmx.min.js asset and same-origin rules apply (air on :8083).
    await page.route("**/__fixture__/**", (route) => {
      if (route.request().method() === "GET") {
        return route.fulfill({
          status: 200,
          contentType: "text/html",
          body: FIXTURE_PAGE,
        });
      }
      return route.fulfill({
        status: 200,
        contentType: "text/html",
        body: loadRestartAlertFragment(),
      });
    });

    await page.goto("/__fixture__/config-restart-alert.html");

    // HTMX is loaded from the app's static assets; wait until it is active.
    await page.waitForFunction(() => {
      return (window as unknown as { htmx?: unknown }).htmx !== undefined;
    });

    // Pre-save state: badge hidden, success target empty.
    await expect(page.locator("#config-restart-badge")).toHaveClass(/hidden/);
    await expect(page.locator("#config-success-message")).toBeEmpty();

    await page.locator("#trigger-config-save").click();

    // Post-swap: the success alert is the main swap content ...
    await expect(page.locator("#config-success-message")).toBeVisible({
      timeout: 5000,
    });
    await expect(page.locator("#config-success-message")).toContainText(
      "Settings saved. Server restart required",
    );

    // ... and exactly one badge remains, OOB-swapped without `hidden`.
    // If the badge lost OOB, a second badge would be injected as main swap
    // content and the original hidden one would stay hidden (count > 1).
    await expect(page.locator("#config-restart-badge")).toHaveCount(1);
    await expect(page.locator("#config-restart-badge")).not.toHaveClass(
      /hidden/,
    );
    await expect(page.locator("#config-restart-badge")).toHaveText(
      "Restart Required",
    );
  });
});
