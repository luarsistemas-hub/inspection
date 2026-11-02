import { defineConfig } from "vitest/config";
import { fileURLToPath } from "node:url";

// The design system is linked from the monorepo and has its own React copy; dedupe keeps one React so its hooks work in component tests.
export default defineConfig({ resolve: { alias: { "@": fileURLToPath(new URL("./src", import.meta.url)) }, dedupe: ["react", "react-dom"] }, test: { environment: "jsdom", include: ["tests/unit/**/*.test.{ts,tsx}"] } });
