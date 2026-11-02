export type VoicePolicyLimits = {
  tenantNoteLimit: number | "";
  tenantAudioLimitMs: number | "";
  inspectionNoteLimit: number | "";
  inspectionAudioLimitMs: number | "";
};

const positiveInteger = (value: number | "") => typeof value === "number" && Number.isInteger(value) && value > 0;
const positiveMinuteCeiling = (value: number | "") => positiveInteger(value) && Number(value) % 60_000 === 0;

/** Voice admission requires all tenant and inspection ceilings to be positive integers. */
export function hasCompleteVoicePolicyLimits(limits: VoicePolicyLimits): boolean {
  return positiveInteger(limits.tenantNoteLimit)
    && positiveMinuteCeiling(limits.tenantAudioLimitMs)
    && positiveInteger(limits.inspectionNoteLimit)
    && positiveMinuteCeiling(limits.inspectionAudioLimitMs);
}

export function voicePolicyMissingLimits(limits: VoicePolicyLimits): string[] {
  return ([
    ["tenantNoteLimit", "limite de anotações da imobiliária"],
    ["tenantAudioLimitMs", "limite de áudio da imobiliária"],
    ["inspectionNoteLimit", "limite de anotações por vistoria"],
    ["inspectionAudioLimitMs", "limite de áudio por vistoria"],
  ] as const)
    .filter(([key]) => key === "tenantAudioLimitMs" || key === "inspectionAudioLimitMs"
      ? !positiveMinuteCeiling(limits[key])
      : !positiveInteger(limits[key]))
    .map(([, label]) => label);
}
