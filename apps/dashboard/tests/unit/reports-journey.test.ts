import { describe, expect, it } from "vitest";
import { isCurrentReportListRequest } from "@/features/dashboard/reports-journey";

describe("ReportsJourney", () => {
  it("UT-055 keeps the active newer search when an older request resolves later", () => {
    const olderSearchRequest = 4;
    const newerSearchRequest = 5;

    expect(isCurrentReportListRequest(olderSearchRequest, newerSearchRequest)).toBe(false);
    expect(isCurrentReportListRequest(newerSearchRequest, newerSearchRequest)).toBe(true);
  });
});
