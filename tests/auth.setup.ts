import fs from "fs";
import path from "path";

import { test as setup } from "@playwright/test";

import { loginViaUI } from "./helpers";

const authFile = path.join("tmp", "playwright", ".auth", "admin.json");

setup("authenticate admin for serial suite", async ({ page }) => {
  fs.mkdirSync(path.dirname(authFile), { recursive: true });
  await loginViaUI(page);
  await page.context().storageState({ path: authFile });
});
