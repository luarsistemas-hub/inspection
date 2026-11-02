import { describe, expect, it } from "vitest";
import { hasCompleteVoicePolicyLimits, voicePolicyMissingLimits } from "@/features/admin/voice-policy-validation";

describe("voice policy ceilings", () => {
  it("requires all four positive integer ceilings before admission can be enabled", () => {
    const valid = { tenantNoteLimit: 100, tenantAudioLimitMs: 300 * 60_000, inspectionNoteLimit: 20, inspectionAudioLimitMs: 30 * 60_000 };
    expect(hasCompleteVoicePolicyLimits(valid)).toBe(true);
    expect(hasCompleteVoicePolicyLimits({ ...valid, tenantNoteLimit: 0 })).toBe(false);
    expect(hasCompleteVoicePolicyLimits({ ...valid, inspectionAudioLimitMs: 1.5 })).toBe(false);
    expect(hasCompleteVoicePolicyLimits({ ...valid, tenantAudioLimitMs: 90_001 })).toBe(false);
    expect(voicePolicyMissingLimits({ ...valid, tenantAudioLimitMs: "", inspectionNoteLimit: "" })).toEqual(["limite de áudio da imobiliária", "limite de anotações por vistoria"]);
  });
});
