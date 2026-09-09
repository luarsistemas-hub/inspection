import { beforeEach, describe, expect, it, vi } from "vitest";
import { beginPKCE, takePKCE } from "@/auth/pkce";
import { safeAdminPath } from "@/auth/return-path";
import { clearProtectedContext, clearSession, getAccessToken, hasAdminAccess, hasDashboardAccess, restoreMembershipContext, setMembershipContext, setSession } from "@/auth/session";

describe("Admin authentication safety", () => {
  beforeEach(() => { sessionStorage.clear(); clearSession(); vi.stubGlobal("location", { origin: "http://localhost:3000", assign: vi.fn() }); });

  it("UT-053 binds Admin PKCE to state and restores one owned return path", async () => {
    await beginPKCE("https://id.example/authorize", "/assets?filter=active");
    const state = new URL(vi.mocked(location.assign).mock.calls[0][0] as string).searchParams.get("state");
    expect(takePKCE(state)).toMatchObject({ returnTo: "/assets?filter=active" });
    expect(takePKCE(state)).toBeUndefined();
  });

  it("UT-054 rejects absolute, cross-origin, and unowned return paths", () => {
    expect(safeAdminPath("https://evil.example/access")).toBe("/organization");
    expect(safeAdminPath("//evil.example/access")).toBe("/organization");
    expect(safeAdminPath("/inspections")).toBe("/organization");
  });

  it("UT-055 clears in-memory access after a revoked or inactive session", () => {
    setSession("admin-token", { tenantId: "tenant", tenantName: "Tenant", entitlements: ["ADMIN", "DASHBOARD"], roles: ["TENANT_ADMIN"] });
    expect(hasAdminAccess()).toBe(true);
    clearSession();
    expect(getAccessToken()).toBeUndefined();
    expect(hasAdminAccess()).toBe(false);
  });

  it("allows delegated administrative roles in Admin but reserves Dashboard for tenant administrators", () => {
    expect(hasAdminAccess({ tenantId: "tenant", tenantName: "Tenant", entitlements: ["ADMIN", "DASHBOARD"], roles: ["TENANT_ADMIN"] })).toBe(true);
    expect(hasAdminAccess({ tenantId: "tenant", tenantName: "Tenant", entitlements: ["ADMIN"], roles: ["ACCESS_ADMIN"] })).toBe(true);
    expect(hasAdminAccess({ tenantId: "tenant", tenantName: "Tenant", entitlements: ["ADMIN", "DASHBOARD"], roles: ["MANAGER"] })).toBe(false);
    expect(hasDashboardAccess({ tenantId: "tenant", tenantName: "Tenant", entitlements: ["ADMIN", "DASHBOARD"], roles: ["TENANT_ADMIN"] })).toBe(true);
    expect(hasDashboardAccess({ tenantId: "tenant", tenantName: "Tenant", entitlements: ["ADMIN", "DASHBOARD"], roles: ["ACCESS_ADMIN"] })).toBe(false);
  });

  it("UT-009 persists membership context per tab and clears it before a new protected request", () => {
    setSession("admin-token", { tenantId: "tenant", tenantName: "Tenant", entitlements: ["ADMIN"], roles: ["ACCESS_ADMIN"] });
    setMembershipContext("membership-1", "BUSINESS_UNIT");
    expect(restoreMembershipContext()).toEqual({ membershipId: "membership-1", scope: "BUSINESS_UNIT" });
    clearProtectedContext();
    expect(restoreMembershipContext()).toBeUndefined();
  });
});
