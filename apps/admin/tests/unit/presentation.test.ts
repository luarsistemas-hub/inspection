import { describe, expect, it } from "vitest";
import { presentAdminRole, presentAdminScope, presentAdminStatus } from "@/features/admin/presentation";
import { formatReportedCost } from "@/features/llm-usage/llm-usage-page";

describe("admin presenters", () => {
  it("localizes roles, scope, statuses, and unknown fallbacks", () => {
    expect(presentAdminRole("TENANT_ADMIN")).toBe("Administrador da imobiliária");
    expect(presentAdminScope("ASSET")).toBe("Imóvel");
    expect(presentAdminStatus("ACTIVE")).toBe("Ativa");
    expect(presentAdminStatus("Nenhuma regra configurada")).toBe("Nenhuma regra configurada");
    expect(presentAdminStatus("Indisponível")).toBe("Indisponível");
    expect(presentAdminStatus("FUTURE")).toBe("Situação não reconhecida");
  });
});

describe("C07 LLM cost presentation", () => {
  it("UT-048 keeps unavailable cost distinct from zero", () => {
    expect(formatReportedCost(null)).toBe("Não informado");
    expect(formatReportedCost(0)).toBe("0.00000000");
  });
});
