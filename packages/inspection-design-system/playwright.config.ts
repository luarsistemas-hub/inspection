import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "tests/browser",
  use: { baseURL: "http://127.0.0.1:4175" },
  webServer: {
    command: "vite tests/browser --host 127.0.0.1 --port 4175",
    port: 4175,
    reuseExistingServer: !process.env.CI
  }
});
