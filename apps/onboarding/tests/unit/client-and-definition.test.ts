import { afterEach, describe, expect, it, vi } from "vitest";
import { getOnboardingCsrfToken, setOnboardingCsrfToken } from "@/auth/onboarding-session";
import { graphql, mapUserErrors, uploadReferencePhoto } from "@/graphql/client";
import { OnboardingDefinitionDocument } from "@/graphql/generated";
import { isSupportedDefinition, nextConfirmedStep, resolveResume, sortedSteps, validateStep, valuesForParticipantMode } from "@/features/onboarding/definition";
import { attentionItemError, isOriginUploadLocked, originUploadError, type OriginUpload } from "@/features/onboarding/origin-upload";

const definition = {
  schemaVersion: 1, version: 4, segment: "REAL_ESTATE", segmentVersion: "real-estate-v1", originModes: [],
  steps: [
    { key: "property", label: "Imóvel", position: 2, required: true, fields: [] },
    { key: "agency", label: "Imobiliária", position: 1, required: true, fields: [] },
  ],
};

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

  it("logs GraphQL operation errors with the server correlation details", async () => {
    const errors = [{ message: "operation failed", extensions: { code: "INVALID_STATE", field: "input", correlationId: "correlation-4" } }];
    const log = vi.spyOn(console, "error").mockImplementation(() => undefined);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ errors }), { status: 200 })));

    await expect(graphql(OnboardingDefinitionDocument, { segment: "REAL_ESTATE" })).rejects.toMatchObject({ code: "INVALID_STATE" });

    expect(log).toHaveBeenCalledWith("[GraphQL] query OnboardingDefinition failed", { errors });
    log.mockRestore();
  });

  it("sends the actual reference photo privately and requires a confirmed media ID", async () => {
    setOnboardingCsrfToken("csrf-proof");
    const fetch = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ mediaId: "media-1" }), { status: 200 })).mockResolvedValueOnce(new Response(JSON.stringify({ message: "upload failed" }), { status: 503 }));
    vi.stubGlobal("fetch", fetch);
    const file = new File(["photo bytes"], "quarto.jpg", { type: "image/jpeg" });
    await expect(uploadReferencePhoto(file, "Quarto", ["Cafeteira"], "photo-1")).resolves.toBe("media-1");
    const [url, init] = fetch.mock.calls[0] as [URL, RequestInit];
    expect(url.pathname).toBe("/onboarding/reference-photos");
    expect(init.credentials).toBe("include");
    expect(init.headers).toMatchObject({ "X-CSRF-Token": "csrf-proof" });
    const form = init.body as FormData;
    expect(form.get("file")).toBe(file);
    expect(form.get("description")).toBe("Quarto");
    expect(form.get("attentionItems")).toBe('["Cafeteira"]');
    expect(form.get("clientMutationId")).toBe("photo-1");
    await expect(uploadReferencePhoto(file, "Quarto", ["Cafeteira"], "photo-1")).rejects.toMatchObject({ message: "upload failed" });
  });

  it("UT-041 maps a server field error without changing unrelated values", () => {
    const values = { name: "Ana", email: "bad-email" };
    expect(mapUserErrors([{ message: "Informe um e-mail válido.", field: "email", code: "INVALID_INPUT" }])).toEqual({ email: "Informe um e-mail válido." });
    expect(values).toEqual({ name: "Ana", email: "bad-email" });
  });

  it("UT-042 resumes from the server-confirmed current step", () => {
    expect(resolveResume({ currentStep: "property" }, { step: "agency" })).toBe("property");
  });

  it("UT-067 sorts definition steps without mutating the server order", () => {
    const source = definition.steps;
    expect(sortedSteps(definition).map((step) => step.position)).toEqual([1, 2]);
    expect(source.map((step) => step.position)).toEqual([2, 1]);
  });

  it("UT-068 rejects unsupported schema and malformed segment definitions", () => {
    expect(isSupportedDefinition(definition)).toBe(true);
    expect(isSupportedDefinition({ ...definition, schemaVersion: 3 })).toBe(false);
    expect(isSupportedDefinition({ ...definition, segment: "UNKNOWN" })).toBe(false);
  });

  it("UT-069 rejects delegated responsibility email mismatch", () => {
    const errors = validateStep({ key: "participant", label: "Responsável", position: 1, required: true, fields: [{ key: "mode", label: "Modo", type: "select", required: true, placeholder: null, options: [] }] }, { mode: "DELEGATE", name: "Bia", email: "ana@example.test", emailConfirmation: "bia@example.test" });
    expect(errors.emailConfirmation).toBe("Os e-mails precisam ser iguais.");
  });

  it("copies the verified owner into the participant fields whenever SELF is selected", () => {
    const values = valuesForParticipantMode({ mode: "DELEGATE", name: "Pessoa anterior", email: "old@example.test" }, "SELF", { name: "Ana", email: "ana@example.test" });
    expect(values).toEqual({ mode: "SELF", name: "Ana", email: "ana@example.test" });
  });

  it("UT-070 copies the verified owner name and email for SELF", () => {
    expect(valuesForParticipantMode({}, "SELF", { name: "Ana", email: "ana@example.test" })).toMatchObject({ name: "Ana", email: "ana@example.test" });
  });

  it("UT-071 resumes from the server checkpoint instead of a participant draft", () => {
    const orderedDefinition = { ...definition, steps: [
      { key: "agency", label: "Agência", position: 1, required: true, fields: [] },
      { key: "property", label: "Imóvel", position: 2, required: true, fields: [] },
      { key: "origin", label: "Fotos", position: 3, required: true, fields: [] },
      { key: "participant", label: "Responsável", position: 4, required: true, fields: [] },
    ] };
    expect(nextConfirmedStep(orderedDefinition, { state: "AGENCY_SAVED", currentStep: "agency" })).toBe("property");
    expect(nextConfirmedStep(orderedDefinition, { state: "PROPERTY_SAVED", currentStep: "origin" })).toBe("participant");
  });

  it("validates required dynamic controls before a checkpoint", () => {
    const errors = validateStep({ key: "agency", label: "Agência", position: 1, required: true, fields: [{ key: "name", label: "Nome", type: "text", required: true, placeholder: null, options: [] }] }, {});
    expect(errors.name).toBeTruthy();
  });

  it("UT-072 rejects a missing description and more than 20 attention items", () => {
    const file = new File(["photo"], "front.jpg", { type: "image/jpeg" });
    const uploads = [{ id: "photo", file, description: "", attentionItems: [], sending: false, failed: false } satisfies OriginUpload];
    expect(originUploadError(uploads)).toBeTruthy();
    expect(attentionItemError("x".repeat(81), [])).toContain("80");
    expect(attentionItemError("vigésimo primeiro", Array.from({ length: 20 }, (_, index) => `item-${index}`))).toContain("20");
  });

  it("UT-073 rejects attention-item duplicates case insensitively", () => {
    expect(attentionItemError("Cadeira", ["cadeira"])).toBe("Este item já foi adicionado.");
  });

  it("UT-074 locks description and attention metadata after media confirmation", () => {
    expect(isOriginUploadLocked({ sending: false })).toBe(false);
    expect(isOriginUploadLocked({ sending: true })).toBe(true);
    expect(isOriginUploadLocked({ sending: false, mediaId: "media-1" })).toBe(true);
  });
});
