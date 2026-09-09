import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { beginPKCE, takePKCE } from "@/auth/pkce";
import { safeDashboardPath } from "@/auth/return-path";
import { clearSession, getAccessToken, getProtectedStateGeneration, hasDashboardAccess, selectMembership, setSession } from "@/auth/session";
import { graphql } from "@/graphql/client";
import { DashboardGateDocument } from "@/graphql/generated";

describe("Dashboard auth and transport", () => {
  beforeEach(() => { sessionStorage.clear(); clearSession(); vi.stubGlobal("location", { origin: "http://localhost:3002", assign: vi.fn() }); });
  afterEach(() => { vi.unstubAllGlobals(); clearSession(); });
  it("uses an inspection-dashboard PKCE state and owned return route", async () => { await beginPKCE("https://id.example/authorize", "/portfolio?assetId=a"); const url = new URL(vi.mocked(location.assign).mock.calls[0][0] as string); expect(url.searchParams.get("client_id")).toBe("inspection-dashboard"); expect(takePKCE(url.searchParams.get("state"))).toMatchObject({ returnTo: "/portfolio?assetId=a" }); expect(safeDashboardPath("https://evil.example")).toBe("/tenants/current"); });
  it("revocation immediately removes the local capability state", () => { setSession("token", { tenantId: "t", tenantName: "T", tenantStatus: "ACTIVE", entitlements: ["DASHBOARD"], roles: ["VIEWER"], scopes: [] }); expect(hasDashboardAccess()).toBe(true); clearSession(); expect(getAccessToken()).toBeUndefined(); expect(hasDashboardAccess()).toBe(false); });
  it("UT-072 sends only an in-memory Dashboard token and selected membership with omitted credentials", async () => { setSession("dashboard-token"); selectMembership("membership-a"); const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { me: { identityId: "1" } } }), { status: 200 })); vi.stubGlobal("fetch", fetch); await graphql(DashboardGateDocument, {}); expect(fetch).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ credentials: "omit", headers: expect.objectContaining({ Authorization: "Bearer dashboard-token", "X-Inspection-Membership-ID": "membership-a" }) })); });
  it("clears protected state before a different membership can load", () => { setSession("dashboard-token", { tenantId: "t", tenantName: "T", tenantStatus: "ACTIVE", entitlements: ["DASHBOARD"], roles: ["VIEWER"], scopes: [] }); const before = getProtectedStateGeneration(); selectMembership("membership-a"); selectMembership("membership-b"); expect(getProtectedStateGeneration()).toBeGreaterThan(before); expect(hasDashboardAccess()).toBe(false); });
  it("maps a non-JSON forbidden response without exposing parser details", async () => { setSession("dashboard-token"); vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("forbidden", { status: 403, headers: { "content-type": "text/plain" } }))); await expect(graphql(DashboardGateDocument, {})).rejects.toMatchObject({ code: "FORBIDDEN", message: "Você não tem permissão para esta operação." }); expect(getAccessToken()).toBeUndefined(); });
});
