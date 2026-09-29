import { defineConfig, devices } from "@playwright/test";

const port = process.env.PLAYWRIGHT_PORT ?? "3112";

export default defineConfig({ testDir: "./tests/e2e", workers: 1, use: { baseURL: process.env.PLAYWRIGHT_BASE_URL ?? `http://localhost:${port}`, trace: "retain-on-failure", screenshot: "only-on-failure" }, webServer: process.env.PLAYWRIGHT_BASE_URL ? undefined : { command: `npm run build && node_modules/.bin/next start --hostname localhost --port ${port}`, url: `http://localhost:${port}`, reuseExistingServer: false }, projects: [{ name: "chromium", use: devices["Desktop Chrome"] }, { name: "webkit", use: devices["Desktop Safari"] }, { name: "mobile", use: devices["Pixel 5"] }] });
