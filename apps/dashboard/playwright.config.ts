import { defineConfig, devices } from "@playwright/test";

export default defineConfig({ testDir: "./tests/e2e", workers: 1, use: { baseURL: process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:3002", trace: "retain-on-failure", screenshot: "only-on-failure" }, webServer: { command: "npm run build && npm run start", url: "http://localhost:3002", reuseExistingServer: !process.env.CI }, projects: [{ name: "chromium", use: devices["Desktop Chrome"] }, { name: "mobile", use: devices["Pixel 5"] }] });
