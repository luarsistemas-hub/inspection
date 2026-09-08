import { describe, expect, it } from "vitest";
import { adminRoutes } from "@/routes";

describe("UT-075 product route ownership", () => {
  it("keeps Admin routes separate from Dashboard and Capture concerns", () => {
    expect(adminRoutes).toEqual(expect.arrayContaining(["/organization", "/access", "/catalogs", "/assets", "/governance", "/audit"]));
    expect(adminRoutes).not.toContain("/reports");
    expect(adminRoutes).not.toContain("/capture/[linkToken]");
  });
});
