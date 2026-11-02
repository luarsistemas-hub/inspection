import { describe, expect, it } from "vitest";
import { describeDisplayedReports, isCurrentReportListRequest, shouldShowEmptyReportState } from "@/features/dashboard/reports-journey";
import { failureText, throwUserErrors } from "@/features/dashboard/baselines/review-model";

describe("ReportsJourney", () => {
  it("UT-055 keeps the active newer search when an older request resolves later", () => {
    const olderSearchRequest = 4;
    const newerSearchRequest = 5;

    expect(isCurrentReportListRequest(olderSearchRequest, newerSearchRequest)).toBe(false);
    expect(isCurrentReportListRequest(newerSearchRequest, newerSearchRequest)).toBe(true);
  });
  it("UT-196 never exposes an arbitrary Error.message from a failed request", () => {
    expect(failureText(new Error("database password leaked"))).toBe("Não foi possível concluir a solicitação. Tente novamente.");
  });
  it("preserves business userError messages through the shared transport formatter", () => {
    let thrown: unknown;
    try { throwUserErrors([{ code: "INVALID_INPUT", message: "Informe um e-mail válido." }]); } catch (error) { thrown = error; }
    expect(failureText(thrown)).toBe("Informe um e-mail válido.");
  });
  it("does not render the empty state alongside a report list error", () => {
    expect(shouldShowEmptyReportState(0, "Não foi possível concluir a solicitação. Tente novamente.")).toBe(false);
    expect(shouldShowEmptyReportState(0)).toBe(true);
    expect(shouldShowEmptyReportState(1, "erro")).toBe(false);
  });
  it("describes loaded report rows as displayed results instead of a total", () => {
    expect(describeDisplayedReports(25, true)).toBe("25 laudos exibidos · há mais laudos para carregar");
    expect(describeDisplayedReports(1, false)).toBe("1 laudo exibido");
  });
});
