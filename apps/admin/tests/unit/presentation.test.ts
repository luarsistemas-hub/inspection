import { describe, expect, it } from "vitest";
import { presentAdminRole, presentAdminScope, presentAdminStatus } from "@/features/admin/presentation";

describe("admin presenters", () => {
  it("localizes roles, scope, statuses, and unknown fallbacks", () => {
    expect(presentAdminRole("TENANT_ADMIN")).toBe("Administrador da imobiliária");
    expect(presentAdminScope("ASSET")).toBe("Imóvel");
    expect(presentAdminStatus("ACTIVE")).toBe("Ativa");
    expect(presentAdminStatus("FUTURE")).toBe("Situação não reconhecida");
  });
});
