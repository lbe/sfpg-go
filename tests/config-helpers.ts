/**
 * Config snapshot/restore helpers for Playwright tests.
 *
 * Mirrors the approach in web-testsuite/helpers_test.go:
 * - snapshot: GET /config/export/download (auth required) → store YAML
 * - restore: POST /config/import/commit with csrf_token + yaml (auth required)
 *
 * Use in test beforeAll/afterAll to isolate config-mutating tests.
 */

import { type APIRequestContext } from "@playwright/test";

const BASE_URL = "http://localhost:8083";

/**
 * Poll /health until the server is accepting requests, or timeout.
 */
async function waitForServerHealth(
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

/**
 * Create an authenticated API context (separate cookie jar from UI tests).
 * Logs in via POST /login to establish a session.
 */
export async function authenticatedAPIContext(
  request: APIRequestContext,
): Promise<APIRequestContext> {
  // Get a CSRF token from the uncached login-form endpoint
  const loginFormResp = await request.get("/login-form");
  const loginFormBody = await loginFormResp.text();
  const csrfMatch = loginFormBody.match(/csrf_token"\s*value="([a-f0-9]+)"/);
  if (!csrfMatch) throw new Error("CSRF token not found in /login-form");
  const csrfToken = csrfMatch[1];

  // Login to get a session cookie (Origin header required by middleware)
  const loginResp = await request.post("/login", {
    headers: {
      "Content-Type": "application/x-www-form-urlencoded",
      Origin: BASE_URL,
    },
    form: {
      username: "admin",
      password: "admin",
      csrf_token: csrfToken,
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
 * Extract a CSRF token from an HTML response body.
 */
function extractCsrfToken(htmlBody: string): string {
  const csrfMatch = htmlBody.match(/csrf_token"\s*value="([a-f0-9]+)"/);
  if (!csrfMatch) throw new Error("CSRF token not found");
  return csrfMatch[1];
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
  // Get CSRF token from config page
  const configResp = await request.get("/config");
  const csrfToken = extractCsrfToken(await configResp.text());

  const resp = await request.post("/config/import/commit", {
    headers: {
      "Content-Type": "application/x-www-form-urlencoded",
      Origin: BASE_URL,
    },
    form: {
      csrf_token: csrfToken,
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
 * Request a server restart via POST /config/restart and poll /health until
 * the server has recovered.
 */
export async function restartServer(request: APIRequestContext): Promise<void> {
  const configResp = await request.get("/config");
  const csrfToken = extractCsrfToken(await configResp.text());

  const resp = await request.post("/config/restart", {
    headers: {
      "Content-Type": "application/x-www-form-urlencoded",
      Origin: BASE_URL,
    },
    form: {
      csrf_token: csrfToken,
    },
  });
  if (!resp.ok()) {
    const body = await resp.text();
    throw new Error(`Config restart request failed: ${resp.status()}: ${body}`);
  }

  // Poll /health until the server process has restarted and is accepting
  // traffic again. The restart goroutine sleeps 500ms before triggering.
  let recovered = false;
  for (let i = 0; i < 30; i++) {
    try {
      const healthResp = await request.get("/health", { timeout: 2000 });
      if (healthResp.status() === 200) {
        recovered = true;
        break;
      }
    } catch {
      // Server still restarting
    }
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  if (!recovered) {
    throw new Error("Server did not recover after restart request");
  }

  // Wait briefly so the new process can finish initializing before the caller
  // proceeds with the next test.
  await new Promise((resolve) => setTimeout(resolve, 1500));
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
