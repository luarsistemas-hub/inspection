import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/e2e", workers: 1, forbidOnly: Boolean(process.env.CI),
  use: { baseURL: process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:3003", trace: "retain-on-failure", screenshot: "only-on-failure" },
  webServer: { command: "npm run dev", url: "http://localhost:3003/healthz", reuseExistingServer: !process.env.CI },
  projects: [{ name: "android", use: { ...devices["Pixel 7"] } }, { name: "webkit-iphone", use: { ...devices["iPhone 13"] } }]
});
