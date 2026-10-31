import { describe, expect, it } from "vitest";
import { presentAdminRole, presentAdminScope, presentAdminStatus } from "@/features/admin/presentation";
import { aggregateCostSummary, costStateLabel, formatReportedCost, isConfirmedZeroCost } from "@/features/llm-usage/llm-usage-page";

describe("admin presenters", () => {
  it("localizes roles, scope, statuses, and unknown fallbacks", () => {
    expect(presentAdminRole("TENANT_ADMIN")).toBe("Administrador da imobiliária");
    expect(presentAdminScope("ASSET")).toBe("Imóvel");
    expect(presentAdminStatus("ACTIVE")).toBe("Ativa");
    expect(presentAdminStatus("INVITED")).toBe("Aguardando ativação");
    expect(presentAdminStatus("Nenhuma regra configurada")).toBe("Nenhuma regra configurada");
    expect(presentAdminStatus("Indisponível")).toBe("Indisponível");
    expect(presentAdminStatus("FUTURE")).toBe("Situação não reconhecida");
  });
});

describe("C07 LLM cost presentation", () => {
  it("UT-048 keeps unavailable cost distinct from zero", () => {
    expect(formatReportedCost(null)).toBe("Não informado");
    expect(formatReportedCost("0")).toBe("US$ 0,00");
    expect(formatReportedCost("0.00000012")).toBe("US$ 0,00000012");
  });
  it("labels confirmed, unknown, uncertain, and unverified cost states", () => {
    expect(costStateLabel("CONFIRMED")).toBe("Custo confirmado");
    expect(costStateLabel("UNKNOWN")).toBe("Custo desconhecido");
    expect(costStateLabel("UNCERTAIN")).toBe("Custo incerto");
    expect(costStateLabel("UNVERIFIED")).toBe("Origem não verificada");
    expect(costStateLabel("ERROR_WITHOUT_COST")).toBe("Erro sem custo informado");
    expect(costStateLabel("NOT_SENT")).toBe("Não enviada (sem custo)");
  });
  it("recognizes zero costs with PostgreSQL numeric scale", () => {
    expect(isConfirmedZeroCost("0.000000000000")).toBe(true);
    expect(isConfirmedZeroCost("000.000")).toBe(true);
    expect(isConfirmedZeroCost("0.00001")).toBe(false);
    expect(isConfirmedZeroCost(null)).toBe(false);
  });
  it("summarizes only present states with singular and plural agreement", () => {
    expect(aggregateCostSummary({ calls: 1, confirmed: 0, uncertain: 0, unknown: 1, error: 0, unverified: 0 })).toBe("1 chamada · 1 custo desconhecido");
    expect(aggregateCostSummary({ calls: 3, confirmed: 2, uncertain: 1, unknown: 4, error: 2, unverified: 1 })).toBe("3 chamadas · 2 custos confirmados · 1 custo incerto · 4 custos desconhecidos · 2 erros sem custo · 1 origem não verificada");
    expect(aggregateCostSummary({ calls: 0, confirmed: 0, uncertain: 0, unknown: 0, error: 0, unverified: 0 })).toBe("0 chamadas");
  });
});
