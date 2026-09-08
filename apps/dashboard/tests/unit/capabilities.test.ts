import { describe, expect, it } from "vitest";
import { composeCapabilities } from "@/features/dashboard/capabilities";

describe("Dashboard capability composition", () => {
  it("UT-056 composes role-specific homes and navigation", () => {
    expect(composeCapabilities({ roles: ["MANAGER"], entitlements: ["DASHBOARD"] }).canPublish).toBe(true);
    expect(composeCapabilities({ roles: ["EMPLOYEE"], entitlements: ["DASHBOARD"] }).canMutate).toBe(true);
    expect(composeCapabilities({ roles: ["VIEWER"], entitlements: ["DASHBOARD"] }).home).toContain("somente leitura");
    expect(composeCapabilities({ roles: ["CUSTOMER_VIEWER"], entitlements: ["DASHBOARD"] }).links.map(([label]) => label)).toEqual(["Portfólio", "Relatórios publicados", "Notificações"]);
  });
  it("UT-057 never emits mutation controls for viewer audiences", () => {
    for (const role of ["VIEWER", "CUSTOMER_VIEWER"]) expect(composeCapabilities({ roles: [role], entitlements: ["DASHBOARD"] }).canMutate).toBe(false);
  });
  it("offers administration only to a tenant admin entitled to both products", () => {
    expect(composeCapabilities({ roles: ["TENANT_ADMIN"], entitlements: ["ADMIN", "DASHBOARD"] }).canUseAdmin).toBe(true);
    expect(composeCapabilities({ roles: ["TENANT_ADMIN"], entitlements: ["DASHBOARD"] }).canUseAdmin).toBe(false);
    expect(composeCapabilities({ roles: ["MANAGER"], entitlements: ["ADMIN", "DASHBOARD"] }).canUseAdmin).toBe(false);
  });
});
