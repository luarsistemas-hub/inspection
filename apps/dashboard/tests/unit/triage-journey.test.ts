import { describe, expect, it } from "vitest";
import { flaggedTriageRows } from "@/features/dashboard/triage-journey";

describe("triage queue classification", () => {
  it("keeps only inspections that require attention or are critical", () => {
    expect(flaggedTriageRows([
      { inspectionId: "normal", classification: "NORMAL" },
      { inspectionId: "attention", classification: "ATTENTION" },
      { inspectionId: "critical", classification: "CRITICAL" },
    ]).map(({ inspectionId }) => inspectionId)).toEqual(["attention", "critical"]);
  });
});
