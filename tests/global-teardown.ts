import { type FullConfig } from "@playwright/test";

import {
  readyAPIContext,
  ensureCacheBatchLoadComplete,
} from "./server-ready-helpers";

/**
 * Global teardown: start cache batch load (if needed) and wait for completion.
 * Runs after all tests so preload exercises a warm, post-test server.
 */
export default async function globalTeardown(
  _config: FullConfig,
): Promise<void> {
  const ctx = await readyAPIContext();
  try {
    await ensureCacheBatchLoadComplete(ctx);
  } finally {
    await ctx.dispose();
  }
}
