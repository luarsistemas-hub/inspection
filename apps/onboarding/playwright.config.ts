import { defineConfig, devices } from "@playwright/test";

const port = process.env.PLAYWRIGHT_PORT ?? "3114";

export default defineConfig({
  testDir: "./tests/e2e",
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  use: { baseURL: process.env.PLAYWRIGHT_BASE_URL ?? `http://localhost:${port}`, trace: "retain-on-failure", screenshot: "only-on-failure" },
  webServer: process.env.PLAYWRIGHT_BASE_URL ? undefined : { command: `npm run build && node_modules/.bin/next start --hostname localhost --port ${port}`, url: `http://localhost:${port}`, timeout: 180_000, reuseExistingServer: false, env: { ...process.env, NEXT_PUBLIC_TURNSTILE_SITE_KEY: process.env.NEXT_PUBLIC_TURNSTILE_SITE_KEY ?? "1x00000000000000000000AA" } },
  projects: [
    { name: "viewport-320", use: { ...devices["iPhone SE"], viewport: { width: 320, height: 844 } } },
    { name: "viewport-360", use: { ...devices["Pixel 7"], viewport: { width: 360, height: 800 } } },
    { name: "viewport-768", use: { ...devices["iPad (gen 7)"], viewport: { width: 768, height: 900 } } },
    { name: "desktop-1440", use: { ...devices["Desktop Chrome"], viewport: { width: 1440, height: 900 } } },
  ],
});
