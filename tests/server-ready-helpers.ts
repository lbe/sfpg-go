/**
 * Shared helpers for Playwright global setup/teardown:
 * health check, discovery wait, and cache batch load wait.
 *
 * Metrics are fetched from the authenticated GET /dashboard HTML page
 * rather than a JSON API endpoint. Values are extracted by element ID
 * per cmd/sfpg-go-dashboard/parser/dashboard.go (canonical reference).
 */
import { request, type APIRequestContext } from "@playwright/test";

import { disableLoginRateLimit } from "./config-helpers";

export const BASE_URL = "http://localhost:8083";
const HEALTH_TIMEOUT_MS = 30_000;
const HEALTH_INTERVAL_MS = 500;
const MODULE_TIMEOUT_MS = 120_000;
const MODULE_INTERVAL_MS = 1_000;

// ─────────────────────────────────────────────────────────────────────
// HTML-parsed dashboard metrics (internal helper types)
// ─────────────────────────────────────────────────────────────────────

/** Internal parsed dashboard metrics used by the wait loops. */
interface ParsedDashboardMetrics {
  totalFound: number;
  inFlight: number;
  httpCacheEnabled: boolean;
  batchIsRunning: boolean;
  batchDoneCount: number;
  batchTotal: number;
}

/**
 * Fetch the dashboard HTML, extract key metric values by element ID,
 * and return them as structured data.
 *
 * Element IDs follow the canonical reference from
 * cmd/sfpg-go-dashboard/parser/dashboard.go.
 */
async function fetchDashboardMetrics(
  ctx: APIRequestContext,
): Promise<ParsedDashboardMetrics> {
  const resp = await ctx.get("/dashboard");
  if (resp.status() !== 200) {
    throw new Error(
      `GET /dashboard failed: ${resp.status()} ${await resp.text()}`,
    );
  }
  const html = await resp.text();

  function extractText(id: string): string {
    const re = new RegExp(`id="${id}"[^>]*>([\\s\\S]*?)</`);
    const m = html.match(re);
    return m ? m[1].trim() : "";
  }

  function parseNum(text: string): number {
    return parseInt(text.replace(/,/g, ""), 10) || 0;
  }

  const totalFound = parseNum(extractText("fp-total"));
  const inFlight = parseNum(extractText("fp-inflight"));

  const httpStatus = extractText("http-status");
  const httpCacheEnabled = httpStatus === "Enabled";

  const batchStatus = extractText("batch-status");
  const batchIsRunning = batchStatus === "Running";

  // batch-progress format: "doneCount / expectedTotal" (both may include commas)
  const progressText = extractText("batch-progress");
  let batchDoneCount = 0;
  let batchTotal = 0;
  if (progressText) {
    const parts = progressText.split("/");
    if (parts.length >= 2) {
      batchDoneCount = parseNum(parts[0]);
      batchTotal = parseNum(parts[parts.length - 1]);
    }
  }

  return {
    totalFound,
    inFlight,
    httpCacheEnabled,
    batchIsRunning,
    batchDoneCount,
    batchTotal,
  };
}

// ─────────────────────────────────────────────────────────────────────

async function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export async function waitForHealth(): Promise<void> {
  const ctx = await request.newContext({ baseURL: BASE_URL });
  try {
    const start = Date.now();
    while (Date.now() - start < HEALTH_TIMEOUT_MS) {
      try {
        const resp = await ctx.get("/health", { timeout: 2_000 });
        if (resp.status() === 200) return;
      } catch {
        // Server may still be starting under air.
      }
      await sleep(HEALTH_INTERVAL_MS);
    }
    throw new Error(
      `Server /health did not return 200 within ${HEALTH_TIMEOUT_MS}ms`,
    );
  } finally {
    await ctx.dispose();
  }
}

function isDiscoveryComplete(metrics: ParsedDashboardMetrics): boolean {
  return metrics.totalFound > 0 && metrics.inFlight === 0;
}

export async function ensureDiscoveryComplete(
  ctx: APIRequestContext,
): Promise<void> {
  const start = Date.now();
  while (Date.now() - start < MODULE_TIMEOUT_MS) {
    const metrics = await fetchDashboardMetrics(ctx);
    if (isDiscoveryComplete(metrics)) return;

    if (metrics.inFlight === 0) {
      const resp = await ctx.post("/server/discovery", {
        headers: { Origin: BASE_URL },
      });
      if (resp.status() !== 200) {
        throw new Error(
          `POST /server/discovery failed: ${resp.status()} ${await resp.text()}`,
        );
      }
    }
    await sleep(MODULE_INTERVAL_MS);
  }
  throw new Error(`Discovery did not complete within ${MODULE_TIMEOUT_MS}ms`);
}

export async function ensureCacheBatchLoadComplete(
  ctx: APIRequestContext,
): Promise<void> {
  const start = Date.now();
  let triggered = false;
  while (Date.now() - start < MODULE_TIMEOUT_MS) {
    const metrics = await fetchDashboardMetrics(ctx);

    if (!metrics.httpCacheEnabled) return;

    const allDone = metrics.batchDoneCount >= metrics.batchTotal;

    if (triggered && !metrics.batchIsRunning && allDone) return;

    if (!triggered && !metrics.batchIsRunning) {
      if (allDone && metrics.batchTotal > 0) return;

      const resp = await ctx.post("/server/cache-batch-load", {
        headers: { Origin: BASE_URL },
      });
      if (resp.status() === 200) {
        triggered = true;
      } else if (resp.status() === 409) {
        await sleep(MODULE_INTERVAL_MS);
        continue;
      } else {
        throw new Error(
          `POST /server/cache-batch-load failed: ${resp.status()} ${await resp.text()}`,
        );
      }
    }
    await sleep(MODULE_INTERVAL_MS);
  }
  throw new Error(
    `Cache batch load did not complete within ${MODULE_TIMEOUT_MS}ms`,
  );
}

/** Authenticated API context with login rate limit disabled. */
export async function readyAPIContext(): Promise<APIRequestContext> {
  const ctx = await request.newContext({ baseURL: BASE_URL });
  await disableLoginRateLimit(ctx);
  return ctx;
}
