import { defineConfig, devices } from "@playwright/test";

const port = process.env.PLAYWRIGHT_PORT ?? "3003";

export default defineConfig({
  testDir: "./tests/e2e", workers: 1, forbidOnly: Boolean(process.env.CI),
  use: { baseURL: process.env.PLAYWRIGHT_BASE_URL ?? `http://localhost:${port}`, trace: "retain-on-failure", screenshot: "only-on-failure" },
  webServer: { command: `npx next dev --port ${port}`, url: `http://localhost:${port}/healthz`, reuseExistingServer: !process.env.CI },
  projects: [{ name: "android", use: { ...devices["Pixel 7"] } }, { name: "webkit-iphone", use: { ...devices["iPhone 13"] } }, { name: "desktop-chrome", use: { ...devices["Desktop Chrome"] } }]
});
