import { describe, expect, it } from "vitest";
import { presentAnalysisMode, presentAnalysisStatus, presentFindingCategory, presentNoRelevantChange, presentClassification, presentDashboardStatus, presentInspectionSource, presentReportMode } from "@/features/dashboard/presentation";

describe("dashboard presenters", () => {
  it("presents known codes and hides unknown values", () => {
    expect(presentClassification("NORMAL")).toBe("Sem alertas identificados");
    expect(presentClassification("ATTENTION")).toBe("Requer atenção");
    expect(presentClassification("CRITICAL")).toBe("Crítica");
    expect(presentDashboardStatus("INVITED")).toBe("Convite enviado");
    expect(presentDashboardStatus("FUTURE")).toBe("Situação não reconhecida");
    expect(presentInspectionSource("MANUAL")).toBe("Manual");
    expect(presentReportMode("HISTORICAL")).toBe("Histórico");
    expect(presentAnalysisMode("CURRENT_ONLY")).toBe("Análise atual");
    expect(presentAnalysisStatus("FAILED")).toBe("Falha técnica");
    expect(presentFindingCategory("OBSTRUCTION")).toBe("Obstrução");
    expect(presentNoRelevantChange(null)).toBe("Comparação inconclusiva");
    expect(presentNoRelevantChange(true)).toBe("Sem alteração relevante identificada");
    expect(presentNoRelevantChange(false)).toBe("Alteração relevante identificada");
  });
});
