import { defineConfig, devices } from "@playwright/test";

const port = process.env.PLAYWRIGHT_PORT ?? "3103";

export default defineConfig({
  testDir: "./tests/e2e", workers: 1, forbidOnly: Boolean(process.env.CI),
  outputDir: process.env.PLAYWRIGHT_OUTPUT_DIR ?? "./test-results",
  use: { baseURL: process.env.PLAYWRIGHT_BASE_URL ?? `http://localhost:${port}`, trace: "retain-on-failure", screenshot: "only-on-failure", serviceWorkers: "allow" },
  webServer: { command: `npm run build && mkdir -p .next/standalone/.next && cp -R .next/static .next/standalone/.next/static && cp -R public .next/standalone/public && PORT=${port} HOSTNAME=127.0.0.1 node .next/standalone/server.js`, url: `http://127.0.0.1:${port}/healthz`, reuseExistingServer: false },
  projects: [{ name: "android", use: { ...devices["Pixel 7"] } }, { name: "webkit-iphone", use: { ...devices["iPhone 13"] } }, { name: "desktop-chrome", use: { ...devices["Desktop Chrome"] } }]
});
