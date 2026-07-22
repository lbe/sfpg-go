/**
 * Shared helpers for Playwright global setup/teardown:
 * health check, discovery wait, and cache batch load wait.
 */
import { request, type APIRequestContext } from "@playwright/test";

import { disableLoginRateLimit } from "./config-helpers";

export const BASE_URL = "http://localhost:8083";
const HEALTH_TIMEOUT_MS = 30_000;
const HEALTH_INTERVAL_MS = 500;
const MODULE_TIMEOUT_MS = 120_000;
const MODULE_INTERVAL_MS = 1_000;

interface FileProcessingMetrics {
  total_found: number;
  already_existing: number;
  newly_inserted: number;
  skipped_invalid: number;
  in_flight: number;
}

interface CacheBatchLoadMetrics {
  targets_total: number;
  targets_scheduled: number;
  targets_completed: number;
  targets_failed: number;
  targets_skipped: number;
  in_flight: number;
  is_running: boolean;
  last_started_at: string;
  last_finished_at: string;
}

interface HTTPCacheMetrics {
  enabled: boolean;
  size_bytes: number;
  max_entry_size: number;
  max_total_size: number;
  entry_count: number;
}

interface MetricsSnapshot {
  timestamp: string;
  file_processing: FileProcessingMetrics;
  cache_batch_load: CacheBatchLoadMetrics;
  http_cache: HTTPCacheMetrics;
}

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

async function getMetrics(ctx: APIRequestContext): Promise<MetricsSnapshot> {
  const resp = await ctx.get("/api/metrics");
  if (resp.status() !== 200) {
    throw new Error(
      `GET /api/metrics failed: ${resp.status()} ${await resp.text()}`,
    );
  }
  return resp.json() as Promise<MetricsSnapshot>;
}

function isDiscoveryComplete(metrics: MetricsSnapshot): boolean {
  return (
    metrics.file_processing.total_found > 0 &&
    metrics.file_processing.in_flight === 0
  );
}

function cacheBatchLoadDoneCount(metrics: MetricsSnapshot): number {
  return (
    metrics.cache_batch_load.targets_completed +
    metrics.cache_batch_load.targets_failed +
    metrics.cache_batch_load.targets_skipped
  );
}

export async function ensureDiscoveryComplete(
  ctx: APIRequestContext,
): Promise<void> {
  const start = Date.now();
  while (Date.now() - start < MODULE_TIMEOUT_MS) {
    const metrics = await getMetrics(ctx);
    if (isDiscoveryComplete(metrics)) return;

    if (metrics.file_processing.in_flight === 0) {
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
    const metrics = await getMetrics(ctx);

    if (!metrics.http_cache.enabled) return;

    const doneCount = cacheBatchLoadDoneCount(metrics);
    const allDone = doneCount >= metrics.cache_batch_load.targets_total;

    if (triggered && !metrics.cache_batch_load.is_running && allDone) return;

    if (!triggered && !metrics.cache_batch_load.is_running) {
      if (allDone && metrics.cache_batch_load.targets_total > 0) return;

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
