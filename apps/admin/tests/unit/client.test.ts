import { afterEach, describe, expect, it, vi } from "vitest";
import { AdminLocalError, AdminUserError, graphQLFailureText, graphql, graphqlIdentity, graphqlOptionalPermission } from "@/graphql/client";
import { clearProtectedContext, clearSession, getAccessToken, setMembershipContext, setSession } from "@/auth/session";
import { AdminIdentityDocument } from "@/graphql/generated";

describe("Admin GraphQL transport", () => {
  afterEach(() => { clearProtectedContext(); clearSession(); vi.unstubAllGlobals(); vi.unstubAllEnvs(); });
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
  it("preserves an active session when an optional platform capability is forbidden", async () => {
    setSession("admin-token", { tenantId: "tenant", tenantName: "Tenant", entitlements: ["ADMIN"], roles: ["TENANT_ADMIN"] }); setMembershipContext("membership-1"); vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ errors: [{ message: "Negado", extensions: { code: "FORBIDDEN" } }] }), { status: 200 })));
    await expect(graphqlOptionalPermission(AdminIdentityDocument)).rejects.toMatchObject({ code: "FORBIDDEN" });
    expect(getAccessToken()).toBe("admin-token");
  });
  it("lists identity-owned memberships without sending a tenant selector", async () => {
    setSession("admin-token"); setMembershipContext("stale-membership");
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { me: { memberships: [] }, tenant: null } }), { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    await graphqlIdentity(AdminIdentityDocument);
    const headers = vi.mocked(fetch).mock.calls[0][1]?.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer admin-token");
    expect(headers["X-Inspection-Membership-ID"]).toBeUndefined();
  });
  it("maps a non-JSON expired response to a stable authentication error", async () => {
    setSession("admin-token", { tenantId: "tenant", tenantName: "Tenant", entitlements: ["ADMIN"], roles: ["ACCESS_ADMIN"] }); setMembershipContext("membership-1"); vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("authentication required", { status: 401, headers: { "content-type": "text/plain" } })));
    await expect(graphql(AdminIdentityDocument)).rejects.toMatchObject({ code: "UNAUTHENTICATED", message: "Sua sessão expirou. Entre novamente." });
    expect(getAccessToken()).toBeUndefined();
  });
  it("logs every GraphQL operation error with its server extensions", async () => {
    setSession("admin-token", { tenantId: "tenant", tenantName: "Tenant", entitlements: ["ADMIN"], roles: ["ACCESS_ADMIN"] }); setMembershipContext("membership-1");
    const errors = [{ message: "requirement media limit reached", extensions: { code: "INVALID_STATE", field: "requirementKey", correlationId: "correlation-1" } }];
    const log = vi.spyOn(console, "error").mockImplementation(() => undefined);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ errors }), { status: 200 })));

    await expect(graphql(AdminIdentityDocument)).rejects.toMatchObject({ code: "INVALID_STATE" });

    expect(log).toHaveBeenCalledWith("[GraphQL] query AdminIdentity failed", { errors });
    log.mockRestore();
  });
  it("UT-193 hides internal and unknown server messages and retains a support code", async () => {
    setSession("admin-token");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ errors: [{ message: "sql password leaked", extensions: { code: "INTERNAL", correlationId: "support-1" } }] }), { status: 200 })));
    await expect(graphqlIdentity(AdminIdentityDocument)).rejects.toMatchObject({ message: "Não foi possível concluir a solicitação. Tente novamente.", code: "INTERNAL", correlationId: "support-1" });
    expect(graphQLFailureText({ message: "Não foi possível concluir a solicitação. Tente novamente.", code: "INTERNAL", correlationId: "support-1" })).toBe("Não foi possível concluir a solicitação. Tente novamente. Código de suporte: support-1");
  });
  it("maps transport validation errors without exposing the server message", async () => {
    setSession("admin-token");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ errors: [{ message: "Informe um e-mail válido.", extensions: { code: "INVALID_INPUT" } }] }), { status: 200 })));
    await expect(graphqlIdentity(AdminIdentityDocument)).rejects.toMatchObject({ message: "Revise os dados informados e tente novamente." });
  });
  it("keeps business and local Admin messages while sanitizing internal transport failures", () => {
    expect(graphQLFailureText(new AdminUserError("INVALID_INPUT", "Informe um e-mail válido."))).toBe("Informe um e-mail válido.");
    expect(graphQLFailureText(new AdminLocalError("A versão atual ainda não foi carregada."))).toBe("A versão atual ainda não foi carregada.");
    expect(graphQLFailureText({ code: "INTERNAL", message: "stack trace" })).toBe("Não foi possível concluir a solicitação. Tente novamente.");
  });
  it("maps known transport codes and ignores unavailable correlation IDs", () => {
    expect(graphQLFailureText({ code: "NOT_FOUND", message: "english backend detail" })).toBe("Registro não encontrado ou sem acesso.");
    expect(graphQLFailureText({ code: "CONFLICT", message: "english backend detail", correlationId: "unavailable" })).toBe("Os dados mudaram. Atualize e tente novamente.");
  });
  it("turns network TypeErrors into a stable Portuguese failure", async () => {
    setSession("admin-token");
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("network trace with secret")));
    await expect(graphqlIdentity(AdminIdentityDocument)).rejects.toMatchObject({ code: "NETWORK_ERROR", message: "Não foi possível concluir a solicitação. Tente novamente." });
  });
  it("renews an expiring Admin token before the protected query", async () => {
    vi.stubEnv("NEXT_PUBLIC_OIDC_TOKEN_URL", "https://id.example/token");
    setSession("old", undefined, undefined, "refresh-1", 5);
    setMembershipContext("membership-1");
    const fetch = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ access_token: "new", refresh_token: "refresh-2", expires_in: 300 }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: { me: { id: "1" } } }), { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    await graphql(AdminIdentityDocument);
    expect(fetch).toHaveBeenCalledTimes(2);
    expect((fetch.mock.calls[1][1] as RequestInit).headers).toMatchObject({ Authorization: "Bearer new", "X-Inspection-Membership-ID": "membership-1" });
    expect(getAccessToken()).toBe("new");
  });
  it("keeps credentials on a temporary refresh outage", async () => {
    vi.stubEnv("NEXT_PUBLIC_OIDC_TOKEN_URL", "https://id.example/token");
    setSession("old", undefined, undefined, "refresh-1", 5);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("unavailable", { status: 503 })));
    await expect(graphqlIdentity(AdminIdentityDocument)).rejects.toThrow("Não foi possível renovar");
    expect(getAccessToken()).toBe("old");
  });
});
