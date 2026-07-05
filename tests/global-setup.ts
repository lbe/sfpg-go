import {
  request,
  type FullConfig,
  type APIRequestContext,
} from "@playwright/test";

const BASE_URL = "http://localhost:8083";
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

async function waitForHealth(): Promise<void> {
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

function extractCSRFToken(html: string): string | null {
  const match = html.match(/name=["']csrf_token["']\s+value=["']([^"']+)["']/);
  return match?.[1] ?? null;
}

async function loginAsAdmin(ctx: APIRequestContext): Promise<void> {
  const loginForm = await ctx.get("/login-form");
  if (loginForm.status() !== 200) {
    throw new Error(`GET /login-form failed: ${loginForm.status()}`);
  }
  const html = await loginForm.text();
  const csrfToken = extractCSRFToken(html);
  if (!csrfToken) {
    throw new Error("Could not extract csrf_token from /login-form");
  }

  const resp = await ctx.post("/login", {
    headers: { Origin: BASE_URL },
    form: {
      csrf_token: csrfToken,
      username: "admin",
      password: "admin",
    },
  });
  if (resp.status() !== 200) {
    throw new Error(`Login failed: ${resp.status()} ${await resp.text()}`);
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

async function ensureDiscoveryComplete(ctx: APIRequestContext): Promise<void> {
  const start = Date.now();
  while (Date.now() - start < MODULE_TIMEOUT_MS) {
    const metrics = await getMetrics(ctx);
    if (isDiscoveryComplete(metrics)) return;

    // Discovery has data in flight or no data yet — trigger it once if idle.
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

async function ensureCacheBatchLoadComplete(
  ctx: APIRequestContext,
): Promise<void> {
  const start = Date.now();
  let triggered = false;
  while (Date.now() - start < MODULE_TIMEOUT_MS) {
    const metrics = await getMetrics(ctx);

    // If the HTTP cache is disabled, the batch load manager is not wired up
    // and there is no cache warming work to wait for.
    if (!metrics.http_cache.enabled) return;

    const doneCount = cacheBatchLoadDoneCount(metrics);
    const allDone = doneCount >= metrics.cache_batch_load.targets_total;

    // Once we have triggered a run, wait until it finishes.
    if (triggered && !metrics.cache_batch_load.is_running && allDone) return;

    // Trigger a run the first time we see the manager idle.
    // If a previous run already finished with targets, we are done.
    if (!triggered && !metrics.cache_batch_load.is_running) {
      const doneCount = cacheBatchLoadDoneCount(metrics);
      const allDone = doneCount >= metrics.cache_batch_load.targets_total;
      if (allDone && metrics.cache_batch_load.targets_total > 0) return;

      const resp = await ctx.post("/server/cache-batch-load", {
        headers: { Origin: BASE_URL },
      });
      if (resp.status() === 200) {
        triggered = true;
      } else if (resp.status() === 409) {
        // Discovery is still active; wait and retry.
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

export default async function globalSetup(_config: FullConfig): Promise<void> {
  await waitForHealth();

  const ctx = await request.newContext({ baseURL: BASE_URL });
  try {
    await loginAsAdmin(ctx);
    await ensureDiscoveryComplete(ctx);
    await ensureCacheBatchLoadComplete(ctx);
  } finally {
    await ctx.dispose();
  }
}
