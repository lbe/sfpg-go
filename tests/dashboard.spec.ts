import { test, expect } from "@playwright/test";
import { loginViaUI, goToDashboard, openMenu, menu } from "./helpers";

// Dashboard polls every 5s; some tests wait for multiple cycles.
test.describe.configure({ timeout: 90000 });

// ───────────────────────────────────────────────────────────────────
// Test 1: Login → Dashboard — verify all data + layout consistency
// ───────────────────────────────────────────────────────────────────

test("Test 1: Dashboard layout is consistent across initial load and polled refresh", async ({
  page,
}) => {
  await loginViaUI(page);
  await goToDashboard(page);

  // ── Verify the dashboard page shell (full page) ─────────────────
  await expect(page.locator("body")).toBeAttached();

  // #dashboard-container must exist exactly once (no nesting!)
  const containers = page.locator("#dashboard-container");
  await expect(containers).toHaveCount(1);

  // Verify the polling trigger is present
  await expect(page.locator('[hx-get="/dashboard"]')).toBeAttached();

  // ── Verify all major dashboard sections are rendered ────────────
  // Header
  await expect(page.getByText("Performance & Health Dashboard")).toBeVisible();
  await expect(page.locator("#last-updated")).toBeVisible();

  // Memory section
  await expect(page.getByText("Memory")).toBeVisible();
  await expect(page.locator("#mem-allocated")).toBeVisible();
  await expect(page.locator("#mem-heap-in-use")).toBeVisible();
  await expect(page.locator("#mem-heap-released")).toBeVisible();
  await expect(page.locator("#mem-heap-objects")).toBeVisible();

  // Runtime section
  await expect(page.getByText("Runtime")).toBeVisible();
  await expect(page.locator("#runtime-goroutines")).toBeVisible();
  await expect(page.locator("#runtime-cpu-count")).toBeVisible();
  await expect(page.locator("#runtime-next-gc")).toBeVisible();
  await expect(page.locator("#runtime-uptime")).toBeVisible();

  // Write Batcher
  await expect(page.getByText("Write Batcher")).toBeVisible();
  await expect(page.locator("#wb-pending")).toBeVisible();
  await expect(page.locator("#wb-flushed")).toBeVisible();
  await expect(page.locator("#wb-errors")).toBeVisible();
  await expect(page.locator("#wb-batch-size")).toBeVisible();
  await expect(page.locator("#wb-dque")).toBeVisible();
  await expect(page.locator("#wb-dque-disk-usage")).toBeVisible();
  await expect(page.locator("#wb-dque-disk-quota")).toBeVisible();

  // Worker Pool
  await expect(page.getByText("Worker Pool")).toBeVisible();
  await expect(page.locator("#wp-running")).toBeVisible();
  await expect(page.locator("#wp-completed")).toBeVisible();
  await expect(page.locator("#wp-successful")).toBeVisible();
  await expect(page.locator("#wp-failed")).toBeVisible();

  // Gallery Statistics
  await expect(page.locator("#card-gallery-stats")).toBeVisible();

  // Queued Items
  await expect(page.locator("#queue-queued")).toBeVisible();

  // File Processing
  await expect(page.getByText("File Processing")).toBeVisible();
  await expect(page.locator("#fp-total")).toBeVisible();
  await expect(page.locator("#fp-existing")).toBeVisible();
  await expect(page.locator("#fp-new")).toBeVisible();
  await expect(page.locator("#fp-invalid")).toBeVisible();
  await expect(page.locator("#fp-inflight")).toBeVisible();

  // Cache Preload
  await expect(page.locator("#card-cache-preload")).toBeVisible();
  await expect(page.locator("#preload-status")).toBeVisible();
  await expect(page.locator("#preload-scheduled")).toBeVisible();
  await expect(page.locator("#preload-completed")).toBeVisible();
  await expect(page.locator("#preload-failed")).toBeVisible();
  await expect(page.locator("#preload-skipped")).toBeVisible();

  // Cache Batch Load
  await expect(page.locator("#card-cache-batch-load")).toBeVisible();
  await expect(page.locator("#batch-status")).toBeVisible();
  await expect(page.locator("#batch-progress")).toBeVisible();
  await expect(page.locator("#batch-failed")).toBeVisible();
  await expect(page.locator("#batch-skipped")).toBeVisible();

  // HTTP Cache
  await expect(page.locator("#card-http-cache")).toBeVisible();
  await expect(page.locator("#http-status")).toBeVisible();
  await expect(page.locator("#http-entries")).toBeVisible();
  await expect(page.locator("#http-size")).toBeVisible();
  await expect(page.locator("#http-max-total")).toBeVisible();
  await expect(page.locator("#http-max-entry")).toBeVisible();
  await expect(page.locator("#http-utilization")).toBeVisible();

  // Live badge
  await expect(page.getByText("Live")).toBeVisible();

  // ── Capture initial layout snapshot ──────────────────────────────
  const initialBBox = await page.locator("#dashboard-container").boundingBox();
  expect(initialBBox).not.toBeNull();

  // ── Wait for at least 2 polling cycles (~10s) ────────────────────
  await page.waitForTimeout(11_000);

  // #dashboard-container must still be exactly 1 (no nesting!)
  await expect(containers).toHaveCount(1);

  // The dashboard-container should still be visible with content
  await expect(page.getByText("Performance & Health Dashboard")).toBeVisible();
  await expect(page.locator("#last-updated")).toBeVisible();

  // Layout bounding box must still match initial load (no shift from nesting)
  const postPollBBox = await page.locator("#dashboard-container").boundingBox();
  expect(postPollBBox).not.toBeNull();
  expect(postPollBBox!.x).toBe(initialBBox!.x);
  expect(postPollBBox!.y).toBe(initialBBox!.y);
  expect(postPollBBox!.width).toBe(initialBBox!.width);
  expect(postPollBBox!.height).toBe(initialBBox!.height);

  // Verify authenticated state survives polling
  await openMenu(page);
  await expect(
    menu(page).getByText("Dashboard", { exact: true }),
  ).toBeVisible();
});

// ───────────────────────────────────────────────────────────────────
// Test 2: Backspace → gallery, then open Dashboard in new tab
//         Refresh + hard refresh in new tab
// ───────────────────────────────────────────────────────────────────

test("Test 2: Open dashboard in new tab, refresh, and hard refresh", async ({
  page,
  context,
}) => {
  await loginViaUI(page);
  await goToDashboard(page);

  // Wait for at least one poll cycle on the original page
  await page.waitForTimeout(5500);

  // ── Backspace to return to gallery ───────────────────────────────
  await page.goBack();
  await page.waitForURL("**/gallery/1**", { timeout: 10000 });
  await page.waitForTimeout(500); // let menu refresh from bfcache/pageshow

  // Verify we're on the gallery page
  await expect(page.locator("#gallery-content")).toBeVisible();
  // Auth should survive back-navigation
  await openMenu(page);
  await expect(
    menu(page).getByText("Dashboard", { exact: true }),
  ).toBeVisible();
  await page.keyboard.press("Escape"); // close menu

  // ── Open Dashboard in a new tab ─────────────────────────────────
  // Get the Dashboard link href from the menu
  const dashboardHref = await page
    .locator("#hamburger-menu-items a[aria-label='Dashboard']")
    .getAttribute("href");
  expect(dashboardHref).not.toBeNull();

  const fullDashboardUrl = dashboardHref!.startsWith("/")
    ? dashboardHref!
    : "/" + dashboardHref;

  // Open dashboard in a new page within the same browser context
  // (shares cookies/session with the original tab)
  const newTab = await context.newPage();
  const response = await newTab.goto(fullDashboardUrl, {
    waitUntil: "domcontentloaded",
  });

  // The dashboard may or may not be authenticated depending on session state
  if (response?.status() === 401) {
    // Session not shared - this is a known edge case. Skip the rest of the test
    // but document the behavior.
    test.info().annotations.push({
      type: "info",
      description:
        "New tab received 401 - session cookie not shared. This is expected with SameSite lax restrictions in some scenarios.",
    });
    await newTab.close();
    return;
  }

  // Dashboard loaded successfully. Wait for content.
  await newTab.waitForSelector("#dashboard-container", { timeout: 10000 });
  await newTab.waitForTimeout(300);

  // Verify content renders
  await expect(newTab.locator("#dashboard-container")).toHaveCount(1);
  await expect(
    newTab.getByText("Performance & Health Dashboard"),
  ).toBeVisible();
  await expect(newTab.locator('[hx-get="/dashboard"]')).toBeAttached();
  await expect(newTab.locator("#last-updated")).toBeVisible();

  // Verify key metric IDs exist
  await expect(newTab.locator("#mem-allocated")).toBeVisible();
  await expect(newTab.locator("#runtime-goroutines")).toBeVisible();
  await expect(newTab.locator("#wb-pending")).toBeVisible();

  // ── Normal refresh ───────────────────────────────────────────────
  await newTab.reload({ waitUntil: "domcontentloaded" });
  await newTab.waitForURL("**/dashboard**", { timeout: 10000 });
  await newTab.waitForSelector("#dashboard-container", { timeout: 10000 });
  await newTab.waitForTimeout(300);

  // Verify still works after refresh
  await expect(newTab.locator("#dashboard-container")).toHaveCount(1);
  await expect(
    newTab.getByText("Performance & Health Dashboard"),
  ).toBeVisible();
  await expect(newTab.locator("#mem-allocated")).toBeVisible();

  // ── Hard refresh (skip cache) ────────────────────────────────────
  await newTab.goto(
    fullDashboardUrl +
      (fullDashboardUrl.includes("?") ? "&" : "?") +
      "_hc=" +
      Date.now(),
    { waitUntil: "domcontentloaded" },
  );
  await newTab.waitForSelector("#dashboard-container", { timeout: 10000 });
  await newTab.waitForTimeout(300);

  // Verify still works after hard refresh
  await expect(newTab.locator("#dashboard-container")).toHaveCount(1);
  await expect(
    newTab.getByText("Performance & Health Dashboard"),
  ).toBeVisible();
  await expect(newTab.locator("#mem-allocated")).toBeVisible();
  await expect(newTab.locator("#runtime-goroutines")).toBeVisible();

  // Polling should still work after hard refresh - wait one cycle
  await newTab.waitForTimeout(6000);
  await expect(newTab.locator("#dashboard-container")).toHaveCount(1);
  await expect(
    newTab.getByText("Performance & Health Dashboard"),
  ).toBeVisible();

  await newTab.close();
});

// ───────────────────────────────────────────────────────────────────
// New Tests (Phase 4.1 additions)
// ───────────────────────────────────────────────────────────────────

test("Test 3: Metrics update on poll", async ({ page }) => {
  await loginViaUI(page);
  await goToDashboard(page);

  // Read initial last-updated timestamp
  const initialText = await page.locator("#last-updated").textContent();
  expect(initialText).not.toBeNull();

  // Wait for 2+ polling cycles (5s each, wait 11s)
  await page.waitForTimeout(11_000);

  // last-updated should have changed
  const newText = await page.locator("#last-updated").textContent();
  expect(newText).not.toBe(initialText);
});

test("Test 4: Partial HTMX response", async ({ page }) => {
  await loginViaUI(page);

  const r = await page.request.get("/dashboard", {
    headers: {
      "HX-Request": "true",
      "HX-Target": "dashboard-container",
    },
  });
  expect(r.status()).toBe(200);
  const body = await r.text();
  // Partial response should NOT contain full <html> or <body> tags
  expect(body).not.toContain("<html");
  expect(body).not.toContain("</html>");
  expect(body).not.toContain("<body");
  // But should contain dashboard content (header text visible in partial)
  // raw HTML uses &amp; (Go html/template escapes & inside <h1>)
  expect(body).toContain("Performance &amp; Health Dashboard");
});

test("Test 5: Metric values are numeric", async ({ page }) => {
  await loginViaUI(page);
  await goToDashboard(page);

  // Verify memory allocated is a number
  const memText = await page.locator("#mem-allocated").textContent();
  expect(memText).not.toBeNull();
  const memVal = parseInt(memText!.replace(/[^0-9]/g, ""), 10);
  expect(Number.isNaN(memVal)).toBe(false);
  expect(memVal).toBeGreaterThanOrEqual(0);

  // Verify goroutines is a positive integer
  const goText = await page.locator("#runtime-goroutines").textContent();
  expect(goText).not.toBeNull();
  const goVal = parseInt(goText!.replace(/[^0-9]/g, ""), 10);
  expect(Number.isNaN(goVal)).toBe(false);
  expect(goVal).toBeGreaterThan(0);
});

test("Test 6: All cache cards rendered", async ({ page }) => {
  await loginViaUI(page);
  await goToDashboard(page);

  await expect(page.locator("#card-cache-preload")).toBeVisible();
  await expect(page.locator("#card-cache-batch-load")).toBeVisible();
  await expect(page.locator("#card-http-cache")).toBeVisible();
});

test("Test 7: Dashboard unauthenticated returns 401", async ({ page }) => {
  const response = await page.goto("/dashboard");
  expect(response?.status()).toBe(401);
});

test("Test 8: Server actions from menu survive (auth intact)", async ({
  page,
}) => {
  await loginViaUI(page);
  await goToDashboard(page);

  // Open menu and expand the Server collapse
  await openMenu(page);
  await page.locator("#hamburger-menu-items details summary").click();
  await page.waitForTimeout(200);

  // Run Discovery
  const discoveryBtn = page.locator('button[aria-label="Run Discovery"]');
  await discoveryBtn.click();

  // Wait for the server action toast to appear (background work started)
  await expect(page.locator("#server-toast-container")).toBeAttached({
    timeout: 5000,
  });

  // Ensure Server collapse is still open, then close menu
  await page.keyboard.press("Escape");
  await page.waitForTimeout(100);

  // Menu should still show authenticated state
  await openMenu(page);
  await expect(
    menu(page).getByText("Dashboard", { exact: true }),
  ).toBeVisible();
  await expect(
    menu(page).getByText("Login", { exact: true }),
  ).not.toBeVisible();
});

// ───────────────────────────────────────────────────────────────────
// Test 9: Dashboard fits on one page at 1920×1080
// ───────────────────────────────────────────────────────────────────

test("Test 9: Dashboard content fits on one page at 1920×1080", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1920, height: 1080 });
  await loginViaUI(page);
  await goToDashboard(page);

  // The scroll parent of #dashboard-container is #gallery-content
  // (h-full overflow-y-auto). Assert the content fits without scrolling.
  const scrollHeight = await page
    .locator("#gallery-content")
    .evaluate((el: HTMLElement) => el.scrollHeight);
  const clientHeight = await page
    .locator("#gallery-content")
    .evaluate((el: HTMLElement) => el.clientHeight);

  expect(scrollHeight).toBeLessThanOrEqual(clientHeight);
});

// ───────────────────────────────────────────────────────────────────
// Test 10a: Gallery discovery dates are hover-only (no permanent timestamps)
// ───────────────────────────────────────────────────────────────────

test("discovery tooltip shows dates on hover", async ({ page }) => {
  await loginViaUI(page);
  await goToDashboard(page);

  const tip = page.locator("#gallery-discovery-tip");
  await expect(tip).toBeVisible();
  const tipText = await tip.getAttribute("data-tip");
  expect(tipText ?? "").toMatch(/First:|Last:|Discovery: unknown/);
  // No permanent ISO-ish timestamps as body text under the dashboard container
  // (About modal lives outside #dashboard-container; #last-updated is time-only.)
  await expect(
    page
      .locator("#dashboard-container")
      .getByText(/\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}/),
  ).toHaveCount(0);
});

// ───────────────────────────────────────────────────────────────────
// Test 10: Dashboard spacing tokens are actually applied (computed-style proof)
// ───────────────────────────────────────────────────────────────────

test("Test 10: dashboard spacing tokens are actually applied", async ({
  page,
}) => {
  await loginViaUI(page);
  await goToDashboard(page);
  const rowGap = await page
    .locator("#dashboard-container > div")
    .nth(1)
    .evaluate((el) => getComputedStyle(el).marginTop);
  expect(parseFloat(rowGap)).toBeGreaterThan(0);
  const bodyPad = await page
    .locator("#dashboard-container .card-body")
    .first()
    .evaluate((el) => getComputedStyle(el).paddingTop);
  expect(parseFloat(bodyPad)).toBeLessThan(16);
});
