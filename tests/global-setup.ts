import { type FullConfig } from "@playwright/test";

import {
  waitForHealth,
  readyAPIContext,
  ensureDiscoveryComplete,
} from "./server-ready-helpers";

/**
 * Global setup: health → disable login rate limit → wait for discovery.
 * Cache batch load runs in global teardown after all tests.
 */
export default async function globalSetup(_config: FullConfig): Promise<void> {
  await waitForHealth();

  const ctx = await readyAPIContext();
  try {
    await ensureDiscoveryComplete(ctx);
  } finally {
    await ctx.dispose();
  }
}
