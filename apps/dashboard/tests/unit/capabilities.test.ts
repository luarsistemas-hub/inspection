import { describe, expect, it } from "vitest";
import { isSuperAdmin } from "@/auth/session";
import { composeCapabilities } from "@/features/dashboard/capabilities";

describe("Dashboard capability composition", () => {
  it("UT-051 makes CUSTOMER_VIEWER a customer audience without operational permissions", () => {
    expect(composeCapabilities({ roles: ["CUSTOMER_VIEWER"], entitlements: ["DASHBOARD"] })).toMatchObject({ audience: "customer", canMutate: false, canPublish: false, canPromoteOrigin: false, canUseAdmin: false, canManageTriage: false });
  });
  it("composes role-specific homes and navigation", () => {
    expect(composeCapabilities({ roles: ["MANAGER"], entitlements: ["DASHBOARD"] }).canPublish).toBe(true);
    expect(composeCapabilities({ roles: ["EMPLOYEE"], entitlements: ["DASHBOARD"] }).canMutate).toBe(true);
    expect(composeCapabilities({ roles: ["VIEWER"], entitlements: ["DASHBOARD"] }).home).toContain("somente leitura");
    expect(composeCapabilities({ roles: ["CUSTOMER_VIEWER"], entitlements: ["DASHBOARD"] }).links.map(([label]) => label)).toEqual(["Portfólio", "Laudos publicados", "Notificações"]);
  });
  it("UT-052 never emits mutation or publication controls for VIEWER", () => {
    for (const role of ["VIEWER", "CUSTOMER_VIEWER"]) expect(composeCapabilities({ roles: [role], entitlements: ["DASHBOARD"] })).toMatchObject({ canMutate: false, canPublish: false });
  });
  it("shows reference promotion only to tenant admins and managers", () => {
    expect(composeCapabilities({ roles: ["TENANT_ADMIN"], entitlements: ["DASHBOARD"] }).canPromoteOrigin).toBe(true);
    expect(composeCapabilities({ roles: ["MANAGER"], entitlements: ["DASHBOARD"] }).canPromoteOrigin).toBe(true);
    expect(composeCapabilities({ roles: ["EMPLOYEE"], entitlements: ["DASHBOARD"] }).canPromoteOrigin).toBe(false);
  });
  it("UT-132 keeps unavailable actions hidden without treating the UI as authorization", () => {
    const viewer = composeCapabilities({ roles: ["VIEWER"], entitlements: ["DASHBOARD"] });
    expect(viewer.canMutate).toBe(false);
    expect(viewer.canPublish).toBe(false);
    expect(viewer.links.map(([label]) => label)).toContain("Vistorias");
  });
  it("UT-053 recognizes a super administrator from existing role and entitlement combinations", () => {
    expect(isSuperAdmin({ roles: ["TENANT_ADMIN"], entitlements: ["ADMIN", "DASHBOARD"] })).toBe(true);
    expect(isSuperAdmin({ roles: ["SUPER_ADMIN"], entitlements: ["ADMIN", "DASHBOARD"] })).toBe(false);
    expect(composeCapabilities({ roles: ["TENANT_ADMIN"], entitlements: ["ADMIN", "DASHBOARD"] }).canUseAdmin).toBe(true);
    expect(composeCapabilities({ roles: ["TENANT_ADMIN"], entitlements: ["DASHBOARD"] }).canUseAdmin).toBe(false);
    expect(composeCapabilities({ roles: ["MANAGER"], entitlements: ["ADMIN", "DASHBOARD"] }).canUseAdmin).toBe(false);
  });
});
