import { afterEach, describe, expect, it, vi } from "vitest";
import { graphql } from "@/graphql/client";
import { clearSession, getAccessToken, setMembershipContext, setSession } from "@/auth/session";
import { AdminIdentityDocument } from "@/graphql/generated";

describe("Admin GraphQL transport", () => {
  afterEach(() => { clearSession(); vi.unstubAllGlobals(); });
  it("UT-071 sends only the in-memory Admin token and omits credentials", async () => {
    setSession("admin-token", { tenantId: "tenant", tenantName: "Tenant", entitlements: ["ADMIN"], roles: ["ACCESS_ADMIN"] }); setMembershipContext("membership-1");
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { me: { id: "1" } } }), { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    await graphql(AdminIdentityDocument);
    expect(fetch).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ credentials: "omit", headers: expect.objectContaining({ Authorization: "Bearer admin-token", "X-Inspection-Membership-ID": "membership-1" }) }));
  });
  it("clears a stale token on forbidden data access", async () => {
    setSession("admin-token", { tenantId: "tenant", tenantName: "Tenant", entitlements: ["ADMIN"], roles: ["ACCESS_ADMIN"] }); setMembershipContext("membership-1"); vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ errors: [{ message: "Negado", extensions: { code: "FORBIDDEN" } }] }), { status: 200 })));
    await expect(graphql(AdminIdentityDocument)).rejects.toMatchObject({ code: "FORBIDDEN" });
    expect(getAccessToken()).toBeUndefined();
  });
  it("maps a non-JSON expired response to a stable authentication error", async () => {
    setSession("admin-token", { tenantId: "tenant", tenantName: "Tenant", entitlements: ["ADMIN"], roles: ["ACCESS_ADMIN"] }); setMembershipContext("membership-1"); vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("authentication required", { status: 401, headers: { "content-type": "text/plain" } })));
    await expect(graphql(AdminIdentityDocument)).rejects.toMatchObject({ code: "UNAUTHENTICATED", message: "Sua sessão expirou. Entre novamente." });
    expect(getAccessToken()).toBeUndefined();
  });
});
