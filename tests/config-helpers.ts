/**
 * Config snapshot/restore helpers for Playwright tests.
 *
 * Mirrors the approach in web-testsuite/helpers_test.go:
 * - snapshot: GET /config/export/download (auth required) → store YAML
 * - restore: POST /config/import/commit with yaml (auth required)
 *
 * Use in test beforeAll/afterAll to isolate config-mutating tests.
 */

import {
  expect,
  type APIRequestContext,
  type Locator,
  type Page,
} from "@playwright/test";

import { openMenu } from "./helpers";

const BASE_URL = "http://localhost:8083";

/**
 * Open the config modal on the Performance tab and wait until pool-size
 * controls are interactive (avoids flakes after prior saves in serial runs).
 */
export async function openConfigPerformanceTab(page: Page): Promise<Locator> {
  await openMenu(page);
  await page.locator('a[aria-label="Configuration"]').click();
  await page.waitForSelector("#config-form", { timeout: 5000 });
  await expect(page.locator("#config_modal").first()).toBeChecked({
    timeout: 5000,
  });
  const perfTab = page.locator("#tab-performance-btn");
  await perfTab.scrollIntoViewIfNeeded();
  await expect(perfTab).toBeVisible({ timeout: 5000 });
  await expect(perfTab).toBeEnabled();
  await perfTab.click();
  const poolInput = page.locator('input[name="db_max_pool_size"]');
  await expect(poolInput).toBeVisible({ timeout: 5000 });
  return poolInput;
}

/**
 * Assert the full restart-dialog-open contract after a restart-required save:
 * success alert with restart text, badge visible/not hidden, restart modal
 * toggle checked, and a non-empty restart diff.
 *
 * Shared by every config save path that must open #restart-diff-modal so a
 * broken OOB badge swap (dialog never opens) cannot stay green.
 */
export async function expectRestartDialogOpen(page: Page): Promise<void> {
  await expect(page.locator("#config-success-message")).toBeVisible({
    timeout: 10000,
  });
  await expect(page.locator("#config-success-message")).toContainText(
    "Server restart required",
  );
  await expect(page.locator("#config-restart-badge")).toBeVisible({
    timeout: 5000,
  });
  await expect(page.locator("#config-restart-badge")).not.toHaveClass(/hidden/);
  await expect(page.locator("#restart-diff-modal")).toBeChecked({
    timeout: 5000,
  });
  await expect(page.locator("#restart-diff-content")).not.toBeEmpty();
  await expect(
    page.locator("#restart-diff-content table tbody tr"),
  ).not.toHaveCount(0);
}

/**
 * Poll /health until the server is accepting requests, or timeout.
 */
export async function waitForServerHealth(
  request: APIRequestContext,
  timeoutMs = 30000,
): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const resp = await request.get("/health", { timeout: 2000 });
      if (resp.status() === 200) {
        return;
      }
    } catch {
      // Server still restarting
    }
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  throw new Error("Server did not become healthy in time");
}

function isConnectionRefused(err: unknown): boolean {
  if (!(err instanceof Error)) {
    return false;
  }
  const msg = err.message.toLowerCase();
  return msg.includes("connection refused") || msg.includes("econnrefused");
}

/**
 * Poll GET /gallery/1 until the server stops accepting connections.
 * Mirrors web-testsuite waitForServerDown: call after a restart POST so the
 * next waitForServer sees the new process, not the dying old one.
 */
export async function waitForServerDown(
  request: APIRequestContext,
  timeoutMs = 15000,
): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      await request.get("/gallery/1", { timeout: 2000 });
    } catch (err) {
      if (isConnectionRefused(err)) {
        return true;
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  return false;
}

/**
 * Poll GET /gallery/1 until the server responds with 200.
 */
export async function waitForServer(
  request: APIRequestContext,
  timeoutMs = 15000,
): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const resp = await request.get("/gallery/1", { timeout: 2000 });
      if (resp.status() === 200) {
        return true;
      }
    } catch {
      // Server still restarting
    }
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  return false;
}

/**
 * Wait for a config-triggered restart to complete (down then up).
 */
export async function waitForServerRestart(
  request: APIRequestContext,
): Promise<void> {
  await waitForServerDown(request);
  const recovered = await waitForServer(request, 30000);
  if (!recovered) {
    throw new Error("Server did not recover after restart");
  }
  await new Promise((resolve) => setTimeout(resolve, 500));
}

/**
 * Create an authenticated API context (separate cookie jar from UI tests).
 * Logs in via POST /login to establish a session.
 */
export async function authenticatedAPIContext(
  request: APIRequestContext,
): Promise<APIRequestContext> {
  const loginResp = await request.post("/login", {
    headers: {
      "Content-Type": "application/x-www-form-urlencoded",
      Origin: BASE_URL,
    },
    form: {
      username: "admin",
      password: "admin",
    },
  });
  if (!loginResp.ok()) {
    throw new Error(`Login failed: ${loginResp.status()}`);
  }
  return request;
}

/**
 * Export the current server config as YAML.
 */
export async function exportConfig(
  request: APIRequestContext,
): Promise<string> {
  const resp = await request.get("/config/export/download");
  if (!resp.ok()) {
    throw new Error(`Config export failed: ${resp.status()}`);
  }
  return resp.text();
}

/**
 * Import a YAML config string via /config/import/commit.
 * Returns true when the server reports that a restart is required for the
 * imported changes to take effect.
 */
export async function importConfig(
  request: APIRequestContext,
  yamlContent: string,
): Promise<boolean> {
  const resp = await request.post("/config/import/commit", {
    headers: {
      "Content-Type": "application/x-www-form-urlencoded",
      Origin: BASE_URL,
    },
    form: {
      yaml: yamlContent,
    },
  });
  const body = await resp.text();
  if (!resp.ok()) {
    throw new Error(`Config import failed: ${resp.status()}: ${body}`);
  }

  // The server returns the save-restart alert when RestartRequired is true.
  return body.includes("Server restart required for changes to take effect.");
}

/**
 * Disable per-IP login rate limiting for the Playwright run (mirrors e2eweb
 * TestMain ensureLoginRateLimitDisabled). Parallel UI tests perform many
 * POST /login calls from one IP against the shared dev server on :8083.
 */
export async function disableLoginRateLimit(
  request: APIRequestContext,
): Promise<void> {
  const api = await authenticatedAPIContext(request);
  let yaml = await exportConfig(api);
  if (!/^login-rate-limit-per-ip:\s*0\s*$/m.test(yaml)) {
    if (/^login-rate-limit-per-ip:/m.test(yaml)) {
      yaml = yaml.replace(
        /^login-rate-limit-per-ip:.*$/m,
        "login-rate-limit-per-ip: 0",
      );
    } else {
      yaml = `${yaml.trimEnd()}\nlogin-rate-limit-per-ip: 0\n`;
    }
  }
  // Always commit so ApplyConfig runs SyncLoginRateLimitMax and clears history.
  // DB may already show 0 while the in-memory limiter is still enforcing.
  const restartRequired = await importConfig(api, yaml);
  if (restartRequired) {
    await restartServer(api);
  }
}

/**
 * Re-enable the HTTP cache via /config/import/commit and restart if required.
 * Used by restart tests to leave the server in a clean state.
 */
export async function enableHTTPCache(
  request: APIRequestContext,
): Promise<void> {
  const api = await authenticatedAPIContext(request);
  const resp = await api.get("/config/export/download");
  if (!resp.ok()) {
    throw new Error(`Config export failed: ${resp.status()}`);
  }
  let yaml = await resp.text();
  yaml = yaml.replace(/^http-cache: .*$/m, "http-cache: true");
  const restartRequired = await importConfig(api, yaml);
  if (restartRequired) {
    await restartServer(api);
  }
}

/**
 * Request a server restart via POST /config/restart and wait until the new
 * process is serving /gallery/1.
 */
export async function restartServer(request: APIRequestContext): Promise<void> {
  const resp = await request.post("/config/restart", {
    headers: {
      "Content-Type": "application/x-www-form-urlencoded",
      Origin: BASE_URL,
    },
  });
  if (!resp.ok()) {
    const body = await resp.text();
    throw new Error(`Config restart request failed: ${resp.status()}: ${body}`);
  }

  await waitForServerRestart(request);
}

/**
 * Snapshot config and return an async restore function.
 *
 * Usage in a spec file:
 *
 *   import { makeSnapshotRestore } from "./config-helpers";
 *
 *   const snapshotRestore = makeSnapshotRestore();
 *   test.beforeAll(async ({ request }) => {
 *     await snapshotRestore.snapshot(request);
 *   });
 *   test.afterAll(async ({ request }) => {
 *     await snapshotRestore.restore(request);
 *   });
 */
export function makeSnapshotRestore() {
  let originalYAML = "";

  return {
    async snapshot(request: APIRequestContext) {
      const api = await authenticatedAPIContext(request);
      originalYAML = await exportConfig(api);
      console.log(`📸 Config snapshot: ${originalYAML.length} bytes`);
    },

    async restore(request: APIRequestContext) {
      if (!originalYAML) {
        console.log("⚠️  No config snapshot to restore");
        return;
      }
      try {
        await waitForServerHealth(request);
        const api = await authenticatedAPIContext(request);
        const restartRequired = await importConfig(api, originalYAML);
        if (restartRequired) {
          console.log(
            "🔄 Config restore requires server restart; restarting...",
          );
          await restartServer(api);
        }
        console.log(
          `📸 Config restored from snapshot (${originalYAML.length} bytes)`,
        );
      } catch (err) {
        console.error("⚠️ Config restore failed:", err);
      }
    },
  };
}
