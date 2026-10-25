import { expect, it, vi } from "vitest";
import type { ExternalCaptureBootstrapQuery } from "@/graphql/generated";
import type { CaptureDraft } from "@/pwa/drafts";
import { addedEnvironmentLabel, captureReviewPresentation, duplicateEnvironmentHint, environmentListItems, environmentProgress, isDuplicateEnvironment, validateEnvironmentName } from "@/pwa/environment-flow";
import { analyzeLumaPixels, clipIsTooShort, clipQualityWarnings, dismissQualityWarnings, failOpenQuality, photoQualityWarnings, setQualityWarnings, submitFailOpen } from "@/pwa/quality";
import { mergeUploadedAnswer } from "@/pwa/capture-policy";
import { readyForSubmission } from "@/pwa/drafts";
import { recoverCompletedVideoDrafts, reusableEnvironmentAttempt, unsupportedRecorderMessage } from "@/pwa/video-capture-state";
import { videoDurationReachedLimit } from "@/pwa/video-recorder";
import { CONDITION_LABELS, OBSERVATION_LABELS, contestStatementState, counterpartyCanRespond, counterpartyErrorMessage } from "@/pwa/counterparty-presentation";

type Requirement = ExternalCaptureBootstrapQuery["externalCapture"]["requirements"][number];
type Answer = ExternalCaptureBootstrapQuery["externalCapture"]["answers"][number];
const req = (key: string, name = key): Requirement => ({ key, section: "Ambiente", label: name, instructions: name, required: true, minimumMedia: 1, maximumMedia: 20, descriptionRequired: false, captureSourcePolicy: "CAMERA_DEFAULT", comparisonTarget: "CHECKLIST_ONLY", impossibilityAllowed: true, environmentIdentityId: null, environmentTypeCode: "OTHER", environmentName: name, expected: true, addedDuringCapture: false });
const draft = (key: string, ready: boolean): CaptureDraft => ({ id: key, responsibilityId: "responsibility", blob: new Blob(["x"]), parts: [], metadata: { requirementKey: key, description: "", source: "camera", capturedAt: "2026-01-01T00:00:00Z" }, mediaId: ready ? `media-${key}` : undefined, metadataSaved: ready, sourceSha256: key, sha256: key });
const answers: Answer[] = [];

it("UT-330 environment progress and window keep every item reachable for 40 environments", () => {
  const requirements = Array.from({ length: 40 }, (_, index) => req(`env:${index}`, `Quarto ${index + 1}`));
  const completedAnswers = requirements.slice(0, 10).map((item) => ({ requirementKey: item.key, mediaIds: [`media-${item.key}`], impossibilityReason: null, version: 1 }));
  expect(environmentProgress(requirements, completedAnswers, requirements.slice(0, 10).map((item) => draft(item.key, true))).completed).toBe(10);
  const list = environmentListItems(requirements, completedAnswers, requirements.slice(0, 10).map((item) => draft(item.key, true)), [], {});
  expect(environmentProgress(requirements, completedAnswers, []).total).toBe(40);
  expect(list).toHaveLength(40);
  expect(list.at(-1)?.label).toBe("Quarto 40");
});

it("UT-331 Outro validation rejects whitespace-only names", () => {
  expect(validateEnvironmentName("OTHER", "  \n ")).toBe("Informe o nome do ambiente.");
  expect(validateEnvironmentName("OTHER", "Depósito 🏠")).toBeUndefined();
});

it("UT-332 evaluates darkness, blur, clip duration and minimum photo count", () => {
  const darkPixels = new Uint8ClampedArray(4 * 16 * 16);
  for (let index = 0; index < darkPixels.length; index += 4) { darkPixels[index] = 20; darkPixels[index + 1] = 20; darkPixels[index + 2] = 20; darkPixels[index + 3] = 255; }
  const dark = analyzeLumaPixels(darkPixels, 16, 16);
  expect(dark.meanLuma).toBeLessThan(40);
  expect(photoQualityWarnings(dark, 0, 2)).toEqual(["DARK", "BLURRY", "TOO_FEW_PHOTOS"]);
  expect(clipQualityWarnings(1.99)).toEqual(["SHORT"]);
  expect(clipQualityWarnings(2)).toEqual([]);
});

it("UT-333 quality analysis errors fail open", async () => {
  expect(await failOpenQuality(async () => { throw new Error("canvas unavailable"); })).toBeUndefined();
});

it("warning reporting failures never block capture submission", async () => {
  const submit = vi.fn().mockResolvedValue("submitted");
  await expect(submitFailOpen([{ requirementKey: "env:sala", code: "DARK" }], async () => { throw new Error("warning endpoint unavailable"); }, submit)).resolves.toBe("submitted");
  expect(submit).toHaveBeenCalledOnce();
});

it("UT-334 warning reducer retains a late warning on its environment after navigation", () => {
  const state = setQualityWarnings(setQualityWarnings({}, "env:sala", []), "env:cozinha", ["DARK"]);
  expect(state["env:cozinha"]).toEqual(["DARK"]);
  expect(state["env:sala"]).toEqual([]);
});

it("UT-335 recapture replaces dismissed warnings so the same warning appears again", () => {
  const firstCapture = setQualityWarnings({}, "env:sala", ["DARK"]);
  const dismissed = dismissQualityWarnings({}, "env:sala", firstCapture["env:sala"]);
  const recapture = setQualityWarnings(firstCapture, "env:sala", photoQualityWarnings({ meanLuma: 39.9, laplacianVariance: 80 }, 1, 1));
  expect(recapture["env:sala"]).toContain("DARK");
  expect(dismissed["env:sala"]).toContain("DARK");
});

it("UT-336 readiness blocks pending uploads and exposes unresolved environment names", () => {
  const requirements = [req("env:sala", "Sala"), req("env:cozinha", "Cozinha")];
  const pendingDraft = draft("env:sala", false);
  expect(readyForSubmission([pendingDraft], true, false)).toBe(false);
  expect(environmentProgress(requirements, answers, [pendingDraft]).pending.map((item) => item.environmentName)).toEqual(["Sala", "Cozinha"]);
});

it("UT-337 review handoff view model renders only configured Dashboard links and preparing state", () => {
  expect(captureReviewPresentation(undefined, "https://dashboard.test")).toEqual({ href: undefined, preparing: false });
  expect(captureReviewPresentation({ inspectionId: "i-1", originVersionId: "v-1" }, "https://dashboard.test/")).toEqual({ href: "https://dashboard.test/baselines/review/v-1", preparing: false });
  expect(captureReviewPresentation({ inspectionId: "i-1", originVersionId: null }, "")).toEqual({ href: undefined, preparing: true });
  expect(captureReviewPresentation({ inspectionId: "i-1", originVersionId: null }, "https://dashboard.test").href).toBe("https://dashboard.test/baselines/review/inspection/i-1");
});

it("UT-338 recorder stops when elapsed time reaches the policy maximum", () => {
  expect(videoDurationReachedLimit(89.99, 90)).toBe(false);
  expect(videoDurationReachedLimit(90, 90)).toBe(true);
});

it("UT-339 unsupported recorder guidance offers impossibility instead of a photo fallback", () => {
  expect(unsupportedRecorderMessage(true)).toContain("declare que o ambiente");
  expect(unsupportedRecorderMessage(true)).toContain("Chrome atualizado");
  expect(unsupportedRecorderMessage(false)).toBe("Este navegador não oferece um formato de gravação compatível. Abra o convite no Safari ou Chrome atualizado do aparelho.");
});

it("UT-340 classifies the backend's real duplicate and environment-limit errors", () => {
  expect(duplicateEnvironmentHint("CONFLICT", "DUPLICATE_ENVIRONMENT")).toContain("Quarto 2");
  expect(duplicateEnvironmentHint("INVALID_INPUT", "LIMIT_ENVIRONMENTS")).toContain("60 ambientes");
  expect(duplicateEnvironmentHint("OTHER")).toBeUndefined();
});

it("UT-341 read-only counterparty baseline hides response controls and localizes API errors", () => {
  expect(counterpartyCanRespond(true, "PENDING")).toBe(false);
  expect(counterpartyCanRespond(false, "PENDING")).toBe(true);
  expect(counterpartyErrorMessage({ code: "INVALID_STATE" })).toContain("Atualizamos");
  expect(CONDITION_LABELS.GOOD).toBe("Bom");
  expect(OBSERVATION_LABELS.PARTIAL).toBe("Parcialmente observado");
});

it("UT-342 contest form blocks statements over 4,000 characters and has no attachment model", () => {
  expect(contestStatementState("x".repeat(4000))).toMatchObject({ valid: true, overLimit: false, count: 4000 });
  expect(contestStatementState("x".repeat(4001))).toMatchObject({ valid: false, overLimit: true });
});

it("UT-343 merges an uploaded photo while leaving the next offline part resumable", () => {
  const initial: Answer[] = [{ requirementKey: "env:sala", mediaIds: ["uploaded"], impossibilityReason: null, version: 2 }];
  expect(mergeUploadedAnswer(initial, "env:sala", "uploaded", false)).toEqual(initial);
  expect(mergeUploadedAnswer(initial, "env:sala", "pending", false)).toMatchObject([{ mediaIds: ["uploaded", "pending"] }]);
});

it("UT-344 rejects clips shorter than one second", () => {
  expect(clipIsTooShort(0.99)).toBe(true);
  expect(clipIsTooShort(1)).toBe(false);
});

it("UT-345 recovery removes incomplete recordings and retains completed environment clips", () => {
  const drafts = ["INCOMPLETE", "RECEIVED", "SAVED"].map((state) => ({ ...draft(state, true), state } as unknown as import("@/pwa/video-drafts").VideoDraft));
  expect(recoverCompletedVideoDrafts(drafts).map((item) => item.state)).toEqual(["RECEIVED", "SAVED"]);
  expect(recoverCompletedVideoDrafts(drafts, false).map((item) => item.state)).toEqual(["INCOMPLETE", "RECEIVED", "SAVED"]);
});

it("UT-345 a received clip of one environment is never reused for the next environment", () => {
  const received = { ...draft("env:sala", true), state: "SAVED", requirementKey: "env:sala" } as unknown as import("@/pwa/video-drafts").VideoDraft;
  const admitted = { ...received, mediaId: undefined } as import("@/pwa/video-drafts").VideoDraft;
  expect(reusableEnvironmentAttempt(received, "env:sala")).toBeUndefined();
  expect(reusableEnvironmentAttempt(admitted, "env:cozinha")).toBeUndefined();
  expect(reusableEnvironmentAttempt(admitted, "env:sala")).toBe(admitted);
});

it("UT-346 marks environments added during the inspection", () => {
  expect(addedEnvironmentLabel({ addedDuringCapture: true })).toBe("adicionado na vistoria");
  expect(addedEnvironmentLabel({ addedDuringCapture: false })).toBeUndefined();
});

it("UT-347 confirmation handoff remains available after returning to Capture", () => {
  const handoff = { inspectionId: "i-1", originVersionId: "v-1" };
  expect(captureReviewPresentation(handoff, "https://dashboard.test").href).toContain("/baselines/review/v-1");
});

it("environment name rules count Unicode code points and normalize duplicates by type and name", () => {
  expect(validateEnvironmentName("BEDROOM", "a".repeat(80))).toBeUndefined();
  expect(validateEnvironmentName("BEDROOM", "a".repeat(81))).toContain("80 caracteres");
  expect(validateEnvironmentName("BEDROOM", "  ")).toBeUndefined();
  expect(validateEnvironmentName("OTHER", "😀".repeat(80))).toBeUndefined();
  expect(validateEnvironmentName("OTHER", "😀".repeat(81))).toContain("80 caracteres");
  expect(isDuplicateEnvironment("BEDROOM", "QUARTO  1", [{ typeCode: "BEDROOM", name: "Quarto 1" }])).toBe(true);
  expect(isDuplicateEnvironment("KITCHEN", "Quarto 1", [{ typeCode: "BEDROOM", name: "Quarto 1" }])).toBe(false);
});
