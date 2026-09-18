import { describe, expect, it } from "vitest";
import { unmatchedReportEvidence } from "@/features/dashboard/report-visual";

describe("Report evidence presentation", () => {
  it("keeps evidence with legacy or unknown requirement keys visible", () => {
    const evidence = [
      { id: "reference", requirementKey: "Referência" },
      { id: "current", requirementKey: "origin:current" },
      { id: "matched", requirementKey: "overview" },
    ];

    expect(unmatchedReportEvidence([{ key: "overview" }], evidence).map(({ id }) => id)).toEqual(["reference", "current"]);
  });
});
