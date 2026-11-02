import { afterEach, describe, expect, it, vi } from "vitest";
import { AdminLocalError, graphQLFailureText } from "@/graphql/client";
import { activationErrorMessage, clearActivationProof, isInitialPasswordValid, requestActivationCode, setInitialPassword, verifyActivationCode } from "@/auth/activation";

describe("C07 activation constraints", () => {
  it("requires six characters, an uppercase letter, a special character, and confirmation", () => {
    expect(isInitialPasswordValid("Abc12!", "Abc12!")).toBe(true);
    expect(isInitialPasswordValid("abc12!", "abc12!")).toBe(false);
    expect(isInitialPasswordValid("Abc1!", "Abc1!")).toBe(false);
    expect(isInitialPasswordValid("Abc123", "Abc123")).toBe(false);
    expect(isInitialPasswordValid("Abc12!", "Abc12?")).toBe(false);
  });

  it("skips the password policy in dev", () => {
    vi.stubEnv("NEXT_PUBLIC_STAGE", "dev");
    expect(isInitialPasswordValid("abc", "abc")).toBe(true);
    expect(isInitialPasswordValid("", "")).toBe(false);
    expect(isInitialPasswordValid("   ", "   ")).toBe(false);
    vi.unstubAllEnvs();
  });
});

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

  it("logs GraphQL errors from activation operations", async () => {
    const errors = [{ message: "activation failed", extensions: { code: "INVALID_STATE", correlationId: "correlation-5" } }];
    const log = vi.spyOn(console, "error").mockImplementation(() => undefined);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ errors }), { status: 200 })));

    await expect(requestActivationCode("invitation-token")).rejects.toThrow("Revise os dados informados e tente novamente.");

    expect(log).toHaveBeenCalledWith("[GraphQL] mutation RequestAdminActivationOtp failed", { errors });
    log.mockRestore();
  });

  it("IT-033 keeps activation-specific messages instead of the generic session text", () => {
    expect(activationErrorMessage("SESSION_EXPIRED")).toBe("Este link de ativação é inválido, já foi utilizado ou expirou.");
    expect(activationErrorMessage("CONFLICT")).toBe("Este convite já foi utilizado.");
    expect(activationErrorMessage("RATE_LIMITED")).toBe("Muitas tentativas de ativação foram realizadas. Aguarde alguns minutos e tente novamente.");
    expect(activationErrorMessage("FORBIDDEN")).toBe("Você não tem permissão para esta operação.");
    expect(activationErrorMessage(undefined)).toBe("Não foi possível concluir a solicitação. Tente novamente.");
  });

  it("raises local activation failures that the page can display as-is", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ errors: [{ message: "expired", extensions: { code: "SESSION_EXPIRED" } }] }))));
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const failure = await verifyActivationCode("123456").catch((error: unknown) => error);
    expect(failure).toBeInstanceOf(AdminLocalError);
    expect(graphQLFailureText(failure)).toBe("Este link de ativação é inválido, já foi utilizado ou expirou.");
    vi.restoreAllMocks();
  });
});
