import { describe, expect, it } from "vitest";
import { presentAnalysisMode, presentAnalysisStatus, presentFindingCategory, presentNoRelevantChange, presentClassification, presentDashboardStatus, presentInspectionSource, presentReportPDFStatus } from "@/features/dashboard/presentation";
import { canRequestInspectionRecapture } from "@/features/dashboard/inspection-views";

describe("dashboard presenters", () => {
  it("presents known codes and hides unknown values", () => {
    expect(presentClassification("NORMAL")).toBe("Sem alertas identificados");
    expect(presentClassification("ATTENTION")).toBe("Requer atenção");
    expect(presentClassification("CRITICAL")).toBe("Crítica");
    expect(presentDashboardStatus("INVITED")).toBe("Convite enviado");
    expect(presentDashboardStatus("FUTURE")).toBe("Situação não reconhecida");
    expect(presentInspectionSource("MANUAL")).toBe("Manual");
    expect(presentAnalysisMode("CURRENT_ONLY")).toBe("Análise atual");
    expect(presentAnalysisStatus("FAILED")).toBe("Falha técnica");
    expect(presentFindingCategory("OBSTRUCTION")).toBe("Obstrução");
    expect(presentNoRelevantChange(null)).toBe("Comparação inconclusiva");
    expect(presentNoRelevantChange(true)).toBe("Sem alteração relevante identificada");
    expect(presentNoRelevantChange(false)).toBe("Alteração relevante identificada");
  });

  it("presents PDF preparation and availability in Portuguese", () => {
    expect(presentReportPDFStatus("PENDING")).toBe("Em preparação");
    expect(presentReportPDFStatus("PROCESSING")).toBe("Em geração");
    expect(presentReportPDFStatus("READY")).toBe("Disponível");
    expect(presentReportPDFStatus("FAILED")).toBe("Falha na geração");
  });
});

describe("inspection recapture eligibility", () => {
  it("requires submitted evidence or a completed inspection with evidence", () => {
    expect(canRequestInspectionRecapture({ status: "INVITED", evidenceCount: 0 })).toBe(false);
    expect(canRequestInspectionRecapture({ status: "SUBMITTED", evidenceCount: 0 })).toBe(false);
    expect(canRequestInspectionRecapture({ status: "SUBMITTED", evidenceCount: 1 })).toBe(true);
    expect(canRequestInspectionRecapture({ status: "COMPLETED", evidenceCount: 1 })).toBe(true);
  });
});
