import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { beginPKCE, takePKCE } from "@/auth/pkce";
import { safeDashboardPath } from "@/auth/return-path";
import { accessTokenForRequest, clearSession, getAccessToken, getMembershipId, getProtectedStateGeneration, hasDashboardAccess, selectMembership, setSession } from "@/auth/session";
import { graphql } from "@/graphql/client";
import { DashboardGateDocument } from "@/graphql/generated";

describe("Dashboard auth and transport", () => {
  beforeEach(() => { sessionStorage.clear(); clearSession(); vi.stubGlobal("location", { origin: "http://localhost:3002", assign: vi.fn() }); });
  afterEach(() => { vi.unstubAllGlobals(); vi.unstubAllEnvs(); clearSession(); });
  it("uses an inspection-dashboard PKCE state and owned return route", async () => { await beginPKCE("https://id.example/authorize", "/portfolio?assetId=a"); const url = new URL(vi.mocked(location.assign).mock.calls[0][0] as string); expect(url.searchParams.get("client_id")).toBe("inspection-dashboard"); expect(takePKCE(url.searchParams.get("state"))).toMatchObject({ returnTo: "/portfolio?assetId=a" }); expect(safeDashboardPath("https://evil.example")).toBe("/tenants/current"); });

  it("restores the schedules screen after authentication", () => {
    expect(safeDashboardPath("/schedules")).toBe("/schedules");
  });
  it("revocation immediately removes the local capability state", () => { setSession("token", { tenantId: "t", tenantName: "T", tenantStatus: "ACTIVE", entitlements: ["DASHBOARD"], roles: ["VIEWER"], scopes: [] }); expect(hasDashboardAccess()).toBe(true); clearSession(); expect(getAccessToken()).toBeUndefined(); expect(hasDashboardAccess()).toBe(false); });
  it("UT-072 persists the selected membership per tab and sends it with omitted credentials", async () => { setSession("dashboard-token"); selectMembership("membership-a"); setSession("dashboard-token-after-reload"); expect(getMembershipId()).toBe("membership-a"); expect(sessionStorage.getItem("inspection.dashboard.membership")).toBe("membership-a"); const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { me: { identityId: "1" } } }), { status: 200 })); vi.stubGlobal("fetch", fetch); await graphql(DashboardGateDocument, {}); expect(fetch).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ credentials: "omit", headers: expect.objectContaining({ Authorization: "Bearer dashboard-token-after-reload", "X-Inspection-Membership-ID": "membership-a" }) })); });
  it("clears protected state before a different membership can load", () => { setSession("dashboard-token", { tenantId: "t", tenantName: "T", tenantStatus: "ACTIVE", entitlements: ["DASHBOARD"], roles: ["VIEWER"], scopes: [] }); const before = getProtectedStateGeneration(); selectMembership("membership-a"); selectMembership("membership-b"); expect(getProtectedStateGeneration()).toBeGreaterThan(before); expect(hasDashboardAccess()).toBe(false); });
  it("maps a non-JSON forbidden response without exposing parser details", async () => { setSession("dashboard-token"); vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("forbidden", { status: 403, headers: { "content-type": "text/plain" } }))); await expect(graphql(DashboardGateDocument, {})).rejects.toMatchObject({ code: "FORBIDDEN", message: "Você não tem permissão para esta operação." }); expect(getAccessToken()).toBeUndefined(); });
  it("logs GraphQL operation errors with their correlation details", async () => {
    setSession("dashboard-token");
    const errors = [{ message: "operation failed", extensions: { code: "INVALID_STATE", field: "input", correlationId: "correlation-2" } }];
    const log = vi.spyOn(console, "error").mockImplementation(() => undefined);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ errors }), { status: 200 })));

    await expect(graphql(DashboardGateDocument, {})).rejects.toMatchObject({ code: "INVALID_STATE" });

    expect(log).toHaveBeenCalledWith("[GraphQL] query DashboardGate failed", { errors });
    log.mockRestore();
  });
  it("renews an expiring token once for concurrent requests and rotates the refresh token", async () => {
    vi.stubEnv("NEXT_PUBLIC_OIDC_TOKEN_URL", "https://id.example/token");
    setSession("old", undefined, undefined, "refresh-1", 5);
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ access_token: "new", refresh_token: "refresh-2", expires_in: 300 }), { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    expect(await Promise.all([accessTokenForRequest(), accessTokenForRequest()])).toEqual(["new", "new"]);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(String((fetch.mock.calls[0][1] as RequestInit).body)).toContain("refresh_token=refresh-1");
    expect(getAccessToken()).toBe("new");
    expect(sessionStorage.getItem("inspection.dashboard.refresh_token")).toBeNull();
  });
  it("renews and retries once when the API rejects a token", async () => {
    vi.stubEnv("NEXT_PUBLIC_OIDC_TOKEN_URL", "https://id.example/token");
    setSession("old", undefined, undefined, "refresh-1", 300);
    const fetch = vi.fn()
      .mockResolvedValueOnce(new Response("expired", { status: 401 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ access_token: "new", refresh_token: "refresh-2", expires_in: 300 }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: { me: { identityId: "1" } } }), { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    await graphql(DashboardGateDocument, {});
    expect(fetch).toHaveBeenCalledTimes(3);
    expect((fetch.mock.calls[2][1] as RequestInit).headers).toMatchObject({ Authorization: "Bearer new" });
  });
});
