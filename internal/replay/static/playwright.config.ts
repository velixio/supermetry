import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1,
  timeout: 15_000,
  use: {
    baseURL: "http://127.0.0.1:4177",
    browserName: "chromium",
    headless: true,
    launchOptions: {
      executablePath: process.env.CHROME ?? "/path/to/local/.cache/ms-playwright/chromium_headless_shell-1243/chrome-headless-shell-linux64/chrome-headless-shell",
    },
  },
  webServer: {
    command: "bun run e2e/server.ts",
    url: "http://127.0.0.1:4177/health",
    reuseExistingServer: false,
  },
});
