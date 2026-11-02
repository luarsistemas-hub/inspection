import { describe, expect, it } from "vitest";
import { flaggedTriageRows, reasonLabels } from "@/features/dashboard/triage-journey";

describe("triage queue classification", () => {
  it("keeps only inspections that require attention or are critical", () => {
    expect(flaggedTriageRows([
      { inspectionId: "normal", classification: "NORMAL" },
      { inspectionId: "attention", classification: "ATTENTION" },
      { inspectionId: "critical", classification: "CRITICAL" },
    ]).map(({ inspectionId }) => inspectionId)).toEqual(["attention", "critical"]);
  });
});

describe("triage reason labels", () => {
  // Os 15 códigos que o classificador determinístico emite; o texto é o mesmo do relatório HTML/PDF.
  const classifierCodes = ["NO_COMPARISONS", "PENDING_COMPARISON", "INCONCLUSIVE", "ANALYSIS_FAILED", "MISSING_EVIDENCE", "SKIPPED_STAGE", "EVIDENCE_FLAG", "UNCORRECTED_RECAPTURE", "OBSERVED_CHANGE", "CRITICAL_FINDING", "INVENTORY_ITEM_MISSING", "INVENTORY_CONDITION_WORSENED", "INVENTORY_ITEM_NOT_OBSERVED", "INVENTORY_ENVIRONMENT_NOT_CAPTURED", "INVENTORY_EMPTY_BASELINE"];

  it("explains every reason code the classifier emits", () => {
    for (const code of classifierCodes) expect(reasonLabels[code], code).toBeTruthy();
  });

  it("uses the canonical report wording for the codes that used to diverge", () => {
    expect(reasonLabels.INCONCLUSIVE).toBe("Análise inconclusiva");
    expect(reasonLabels.OBSERVED_CHANGE).toBe("Alteração observada");
    expect(reasonLabels.CRITICAL_FINDING).toBe("Achado crítico");
  });
});
