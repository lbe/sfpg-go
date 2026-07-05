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
 * Create an authenticated API context (separate cookie jar from UI tests).
 * Logs in via POST /login to establish a session.
 */
export async function authenticatedAPIContext(
  request: APIRequestContext,
): Promise<APIRequestContext> {
  // Get a CSRF token from the gallery page
  const galleryResp = await request.get("/gallery/1");
  const galleryBody = await galleryResp.text();
  const csrfMatch = galleryBody.match(/csrf_token"\s*value="([a-f0-9]+)"/);
  if (!csrfMatch) throw new Error("CSRF token not found in /gallery/1");
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
 * Import a YAML config string via /config/import/commit.
 */
export async function importConfig(
  request: APIRequestContext,
  yamlContent: string,
): Promise<void> {
  // Get CSRF token from config page
  const configResp = await request.get("/config");
  const configBody = await configResp.text();
  const csrfMatch = configBody.match(/csrf_token"\s*value="([a-f0-9]+)"/);
  if (!csrfMatch) throw new Error("CSRF token not found in /config");
  const csrfToken = csrfMatch[1];

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
  if (!resp.ok()) {
    const body = await resp.text();
    throw new Error(`Config import failed: ${resp.status()}: ${body}`);
  }
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
        const api = await authenticatedAPIContext(request);
        await importConfig(api, originalYAML);
        console.log(
          `📸 Config restored from snapshot (${originalYAML.length} bytes)`,
        );
      } catch (err) {
        console.error("⚠️ Config restore failed:", err);
      }
    },
  };
}
