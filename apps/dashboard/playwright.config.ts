import { defineConfig, devices } from "@playwright/test";

const port = process.env.PLAYWRIGHT_PORT ?? "3112";
const useLocalStack = process.env.INSPECTION_E2E_AUTH === "true";
const baseURL = process.env.PLAYWRIGHT_BASE_URL ?? (useLocalStack ? "http://localhost:3002" : `http://localhost:${port}`);

export default defineConfig({ testDir: "./tests/e2e", workers: 1, use: { baseURL, trace: "retain-on-failure", screenshot: "only-on-failure" }, webServer: process.env.PLAYWRIGHT_BASE_URL || useLocalStack ? undefined : { command: `npm run build && mkdir -p .next/standalone/.next && cp -R .next/static .next/standalone/.next/static && PORT=${port} HOSTNAME=127.0.0.1 node .next/standalone/server.js`, url: `http://localhost:${port}`, reuseExistingServer: false, env: { NEXT_PUBLIC_OIDC_TOKEN_URL: process.env.NEXT_PUBLIC_OIDC_TOKEN_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/token" } }, projects: [{ name: "chromium", use: devices["Desktop Chrome"] }, { name: "webkit", use: devices["Desktop Safari"] }, { name: "mobile", use: devices["Pixel 5"] }] });
