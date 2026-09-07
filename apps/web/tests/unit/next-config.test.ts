import { afterEach, describe, expect, it, vi } from "vitest";

describe("Next security headers", () => {
  afterEach(() => {
    delete process.env.NEXT_PUBLIC_INSPECTION_API_URL;
    vi.resetModules();
  });

  it("allows the configured inspection API origin in connect-src", async () => {
    process.env.NEXT_PUBLIC_INSPECTION_API_URL = "http://localhost:8180/graphql";

    const { default: config } = await import("../../next.config");
    const headers = await config.headers?.();
    const csp = headers?.[0]?.headers?.find((header) => header.key === "Content-Security-Policy");

    expect(csp?.value).toContain("connect-src 'self' http://localhost:8180 http://localhost:9002");
  });
});
