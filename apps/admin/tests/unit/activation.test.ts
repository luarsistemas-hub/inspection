import { afterEach, describe, expect, it, vi } from "vitest";
import { clearActivationProof, requestActivationCode, setInitialPassword, verifyActivationCode } from "@/auth/activation";

describe("Admin activation transport", () => {
  afterEach(() => { clearActivationProof(); vi.unstubAllGlobals(); });

  it("exchanges the invitation token without sending a password before password setup", async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: { requestAdminActivationOtp: { userErrors: [] } } }), { headers: { "X-CSRF-Token": "activation-proof" } }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: { verifyAdminActivationOtp: { userErrors: [] } } })))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: { setAdminInitialPassword: { userErrors: [] } } })));
    vi.stubGlobal("fetch", fetch);

    await requestActivationCode("invitation-token");
    await verifyActivationCode("123456");
    await setInitialPassword("uma-senha-longa");

    expect(String(fetch.mock.calls[0][1].body)).toContain("RequestAdminActivationOtp");
    expect(String(fetch.mock.calls[0][1].body)).toContain("invitation-token");
    expect(String(fetch.mock.calls[0][1].body)).not.toContain("uma-senha-longa");
    expect((fetch.mock.calls[1][1] as RequestInit).headers).toMatchObject({ "X-CSRF-Token": "activation-proof" });
    expect((fetch.mock.calls[2][1] as RequestInit).headers).toMatchObject({ "X-CSRF-Token": "activation-proof" });
    expect(String(fetch.mock.calls[2][1].body)).toContain("uma-senha-longa");
  });

  it("returns stable user errors without starting PKCE", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { verifyAdminActivationOtp: { userErrors: [{ code: "INVALID_INPUT", field: "code", message: "Código inválido" }] } } }))));
    await expect(verifyActivationCode("000000")).resolves.toEqual([{ code: "INVALID_INPUT", field: "code", message: "Código inválido" }]);
  });

  it("requires an e-mail link when no activation cookie can be restored", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { onboardingSession: null } }))));
    await expect(requestActivationCode()).rejects.toThrow("Abra o link de ativação enviado para o seu e-mail");
  });
});
