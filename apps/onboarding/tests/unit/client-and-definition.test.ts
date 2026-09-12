import { afterEach, describe, expect, it, vi } from "vitest";
import { getOnboardingCsrfToken, setOnboardingCsrfToken } from "@/auth/onboarding-session";
import { graphql, mapUserErrors } from "@/graphql/client";
import { OnboardingDefinitionDocument } from "@/graphql/generated";
import { resolveResume, validateStep } from "@/features/onboarding/definition";

describe("onboarding session client", () => {
  afterEach(() => { setOnboardingCsrfToken(undefined); vi.unstubAllGlobals(); });

  it("UT-040 sends credentials and never serializes cookie or CSRF values", async () => {
    setOnboardingCsrfToken("csrf-proof");
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { onboardingDefinition: {} } }), { status: 200 }));
    vi.stubGlobal("fetch", fetch);

    await graphql(OnboardingDefinitionDocument, { segment: "REAL_ESTATE" });

    const init = fetch.mock.calls[0][1] as RequestInit;
    expect(init.credentials).toBe("include");
    expect(init.headers).toMatchObject({ "X-CSRF-Token": "csrf-proof" });
    expect(String(init.body)).not.toContain("csrf-proof");
    expect(String(init.body)).not.toContain("Cookie");
  });

  it("accepts a rotated CSRF proof only from a response header", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { onboardingDefinition: {} } }), { status: 200, headers: { "X-CSRF-Token": "rotated-proof" } })));
    await graphql(OnboardingDefinitionDocument, { segment: "REAL_ESTATE" });
    expect(getOnboardingCsrfToken()).toBe("rotated-proof");
  });

  it("UT-041 maps a server field error without changing unrelated values", () => {
    const values = { name: "Ana", email: "bad-email" };
    expect(mapUserErrors([{ message: "Informe um e-mail válido.", field: "email", code: "INVALID_INPUT" }])).toEqual({ email: "Informe um e-mail válido." });
    expect(values).toEqual({ name: "Ana", email: "bad-email" });
  });

  it("UT-042 resumes from the server-confirmed current step", () => {
    expect(resolveResume({ currentStep: "property" }, { step: "agency" })).toBe("property");
  });

  it("validates required dynamic controls before a checkpoint", () => {
    const errors = validateStep({ key: "agency", label: "Agência", position: 1, required: true, fields: [{ key: "name", label: "Nome", type: "text", required: true, placeholder: null, options: [] }] }, {});
    expect(errors.name).toBeTruthy();
  });
});
