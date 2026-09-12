import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/e2e",
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  use: { baseURL: process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:3004", trace: "retain-on-failure", screenshot: "only-on-failure" },
  webServer: { command: "npm run build && npm run start", url: "http://localhost:3004", reuseExistingServer: !process.env.CI },
  projects: [
    { name: "mobile-320", use: { ...devices["iPhone SE"] } },
    { name: "mobile-360", use: { ...devices["Pixel 7"] } },
    { name: "tablet-768", use: { ...devices["iPad (gen 7)"] } },
    { name: "desktop-1440", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } } },
  ],
});
