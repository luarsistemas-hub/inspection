import { defineConfig, devices } from "@playwright/test";

export default defineConfig({
  testDir: "./tests/e2e",
  workers: 1,
  use: { baseURL: process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:3010", trace: "retain-on-failure", screenshot: "only-on-failure" },
  webServer: process.env.PLAYWRIGHT_BASE_URL ? undefined : { command: "npm run build && node_modules/.bin/next start --hostname localhost --port 3010", url: "http://localhost:3010", reuseExistingServer: false, env: { NEXT_PUBLIC_OIDC_TOKEN_URL: process.env.NEXT_PUBLIC_OIDC_TOKEN_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/token" } },
  projects: [
    { name: "chromium", use: devices["Desktop Chrome"] },
    { name: "webkit", use: devices["Desktop Safari"] },
    { name: "webkit-iphone", use: devices["iPhone 13"] },
    { name: "android-chromium", use: devices["Pixel 7"] },
  ]
});
