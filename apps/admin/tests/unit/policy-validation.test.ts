import { describe, expect, it } from "vitest";
import { isValidBaselinePolicyDays, isValidSupersededRetentionDays } from "@/features/admin/policy-validation";
import { templateSchemaNotice, buildEnvironmentCaptureV2 } from "@/features/admin/template-v2";
import { preparationFrameMetrics } from "@/features/admin/video-preparation-view";

describe("baseline policy form boundaries", () => {
  it("UT-360 accepts whole days from 1 through 90 only", () => {
    expect([1, 90].every(isValidBaselinePolicyDays)).toBe(true);
    expect([0, 91, 1.5, Number.NaN].some(isValidBaselinePolicyDays)).toBe(false);
  });

  it("UT-361 accepts superseded retention from 1 through 3650 only", () => {
    expect([1, 3650].every(isValidSupersededRetentionDays)).toBe(true);
    expect([0, 3651, 4.5, Number.NaN].some(isValidSupersededRetentionDays)).toBe(false);
  });

  it("UT-362 builds a v2 environment capture definition and keeps v1 read-only", () => {
    expect(buildEnvironmentCaptureV2({ photoMinimum: 2, photoMaximum: 12, videoMaximumClips: 3, suggestedEnvironmentTypes: "LIVING_ROOM, KITCHEN" })).toEqual({ captureLayout: "ENVIRONMENTS", environmentCapture: { photo: { minimum: 2, maximum: 12 }, video: { maximumClips: 3 } }, suggestedEnvironmentTypes: ["LIVING_ROOM", "KITCHEN"] });
    expect(templateSchemaNotice(1, 4)).toContain("v1 sem inventário");
    expect(templateSchemaNotice(1, 4)).toContain("somente leitura");
    expect(templateSchemaNotice(2, 5)).toBe("Versão ativa 5 · v2 com captura por ambiente.");
  });

  it("UT-363 presents sampled, retained and representative frame counts", () => {
    expect(preparationFrameMetrics({ framesSampled: 80, framesRetained: 24, framesRepresentative: 6 })).toBe("Quadros: 80 amostrados · 24 mantidos · 6 enviados à análise");
  });
});
