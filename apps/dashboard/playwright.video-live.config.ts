import { defineConfig, devices } from "@playwright/test";

const timeout = Number(process.env.PLAYWRIGHT_VIDEO_LIVE_TIMEOUT_MS ?? 30 * 60 * 1000);
const baseURL = process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:3002";

export default defineConfig({
  testDir: "./tests/e2e",
  testMatch: "video-report-live.e2e.ts",
  timeout,
  expect: { timeout: 20_000 },
  workers: 1,
  retries: 0,
  fullyParallel: false,
  reporter: "list",
  outputDir: "./test-results/video-live",
  use: {
    baseURL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"], channel: "chrome" } }],
});
