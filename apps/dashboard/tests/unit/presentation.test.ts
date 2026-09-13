import { describe, expect, it } from "vitest";
import { presentClassification, presentDashboardStatus, presentInspectionSource, presentReportMode } from "@/features/dashboard/presentation";

describe("dashboard presenters", () => {
  it("presents known codes and hides unknown values", () => {
    expect(presentClassification("NORMAL")).toBe("Sem alterações relevantes");
    expect(presentClassification("ATTENTION")).toBe("Requer atenção");
    expect(presentClassification("CRITICAL")).toBe("Crítica");
    expect(presentDashboardStatus("INVITED")).toBe("Convite enviado");
    expect(presentDashboardStatus("FUTURE")).toBe("Situação não reconhecida");
    expect(presentInspectionSource("MANUAL")).toBe("Manual");
    expect(presentReportMode("HISTORICAL")).toBe("Histórico");
  });
});
