import { afterEach, describe, expect, it, vi } from "vitest";
import { graphql } from "@/graphql/client";
import { clearSession, getAccessToken, setSession } from "@/auth/session";

describe("Admin GraphQL transport", () => {
  afterEach(() => { clearSession(); vi.unstubAllGlobals(); });
  it("UT-071 sends only the in-memory Admin token and omits credentials", async () => {
    setSession("admin-token");
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { me: { id: "1" } } }), { status: 200 }));
    vi.stubGlobal("fetch", fetch);
    await graphql("query Me { me { identityId } }");
    expect(fetch).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ credentials: "omit", headers: expect.objectContaining({ Authorization: "Bearer admin-token" }) }));
  });
  it("clears a stale token on forbidden data access", async () => {
    setSession("admin-token"); vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ errors: [{ message: "Negado", extensions: { code: "FORBIDDEN" } }] }), { status: 200 })));
    await expect(graphql("query Me { me { identityId } }")).rejects.toMatchObject({ code: "FORBIDDEN" });
    expect(getAccessToken()).toBeUndefined();
  });
});
