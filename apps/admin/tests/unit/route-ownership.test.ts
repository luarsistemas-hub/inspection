import { describe, expect, it } from "vitest";
import { adminRoutes, legacyRedirects } from "@/routes";

describe("UT-075 product route ownership", () => {
  it("keeps Admin routes separate from Dashboard and Capture concerns", () => {
    expect(adminRoutes).toEqual(expect.arrayContaining(["/organization", "/access", "/catalogs", "/assets", "/governance", "/audit"]));
    expect(adminRoutes).not.toContain("/reports");
    expect(adminRoutes).not.toContain("/capture/[linkToken]");
  });
});

describe("UT-165 historical route handling", () => {
  it("redirects known legacy roots once and leaves malformed nested targets owned by the destination", () => {
    const redirects = legacyRedirects("http://dashboard.test", "http://capture.test");
    expect(redirects).toEqual(expect.arrayContaining([
      { source: "/dashboard", destination: "http://dashboard.test/", permanent: false },
      { source: "/capture/:path*", destination: "http://capture.test/capture/:path*", permanent: false },
    ]));
    expect(redirects.every((redirect) => !redirect.destination.startsWith("http://admin.test"))).toBe(true);
  });
});
