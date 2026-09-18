import type { ExternalCaptureBootstrapQuery } from "@/graphql/generated";
import type { CaptureDraft } from "./drafts";

type Requirement = ExternalCaptureBootstrapQuery["externalCapture"]["requirements"][number];
type Answer = ExternalCaptureBootstrapQuery["externalCapture"]["answers"][number];
export type RequirementState = "complete" | "pending" | "blocked" | "missing";

export const guidedRequirements = (requirements: Requirement[]): Requirement[] =>
  requirements.filter((item) => item.comparisonTarget === "FIXED_ORIGIN" && item.key.startsWith("origin:"));

export function requirementState(requirement: Requirement, answers: Answer[], drafts: CaptureDraft[]): RequirementState {
  const answer = answers.find((item) => item.requirementKey === requirement.key);
  if (answer?.impossibilityReason?.trim()) return "complete";
  const matching = drafts.filter((draft) => draft.metadata.requirementKey === requirement.key);
  const blocked = matching.filter((draft) => ["SCREENED", "REJECTED", "PURGED", "ABORTED"].includes(draft.mediaStatus ?? ""));
  const blockedIds = new Set(blocked.map((draft) => draft.mediaId));
  const readyIds = answer?.mediaIds.filter((id) => !blockedIds.has(id) && !matching.some((draft) => draft.mediaId === id && draft.metadataSaved !== true)) ?? [];
  if (readyIds.length >= requirement.minimumMedia) return "complete";
  if (matching.some((draft) => !blocked.includes(draft))) return "pending";
  if (blocked.length) return "blocked";
  return "missing";
}

export function nextGuidedRequirement(requirements: Requirement[], currentKey: string, answers: Answer[], drafts: CaptureDraft[]): string | undefined {
  const current = requirements.findIndex((item) => item.key === currentKey);
  for (let offset = 1; offset < requirements.length; offset += 1) {
    const candidate = requirements[(current + offset) % requirements.length];
    if (requirementState(candidate, answers, drafts) === "missing") return candidate.key;
  }
  return undefined;
}

export const guidedProgress = (requirements: Requirement[], answers: Answer[], drafts: CaptureDraft[]): number =>
  requirements.filter((requirement) => requirementState(requirement, answers, drafts) === "complete").length;
