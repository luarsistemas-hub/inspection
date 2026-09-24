import type { ExternalCaptureBootstrapQuery } from "@/graphql/generated";
import { type CaptureDraft, mediaCountForRequirement } from "@/pwa/drafts";

type CaptureRequirement = ExternalCaptureBootstrapQuery["externalCapture"]["requirements"][number];
type CaptureAnswer = ExternalCaptureBootstrapQuery["externalCapture"]["answers"][number];

const terminalBootstrapStatuses = new Set(["EXPIRED", "REVOKED", "COMPLETED", "INVALIDATED"]);
const blockedMediaStatuses = new Set(["SCREENED", "REJECTED", "PURGED", "ABORTED"]);

export const isTerminalBootstrapStatus = (status: string): boolean => terminalBootstrapStatuses.has(status);
export const isBlockedMediaStatus = (status?: string): boolean => status !== undefined && blockedMediaStatuses.has(status);
export const isFalsePositiveActionable = (status?: string): boolean => status === "SCREENED";

export const requirementsSatisfied = (requirements: CaptureRequirement[], answers: ExternalCaptureBootstrapQuery["externalCapture"]["answers"], drafts: CaptureDraft[]): boolean => requirements.filter((requirement) => requirement.required).every((requirement) => {
  const answer = answers.find((item) => item.requirementKey === requirement.key);
  return Boolean(answer?.impossibilityReason || mediaCountForRequirement(requirement.key, answers, drafts) >= requirement.minimumMedia);
});

export const isGalleryAllowed = (capturePolicy: Record<string, unknown>, requirement?: CaptureRequirement): boolean => capturePolicy.allowGallery === true && requirement?.captureSourcePolicy !== "CAMERA_ONLY";

export const mergeUploadedAnswer = (answers: CaptureAnswer[], requirementKey: string, mediaId: string, blocked: boolean, replacesMediaId?: string): CaptureAnswer[] => {
  const current = answers.find((answer) => answer.requirementKey === requirementKey);
  const retained = current?.mediaIds.filter((id) => id !== replacesMediaId) ?? [];
  if (blocked) {
    if (!current) return [...answers, { requirementKey, mediaIds: [], impossibilityReason: null, version: 1 }];
    const replay = current.mediaIds.includes(mediaId);
    return answers.map((answer) => answer.requirementKey === requirementKey ? { ...answer, mediaIds: retained.filter((id) => id !== mediaId), version: replay ? answer.version : answer.version + 1 } : answer);
  }
  if (!current) return [...answers, { requirementKey, mediaIds: [mediaId], impossibilityReason: null, version: 1 }];
  if (current.mediaIds.includes(mediaId) && replacesMediaId === undefined) return answers;
  return answers.map((answer) => answer.requirementKey === requirementKey ? { ...answer, mediaIds: [...retained.filter((id) => id !== mediaId), mediaId], version: current.mediaIds.includes(mediaId) ? answer.version : answer.version + 1 } : answer);
};
export const buildImpossibilityInput = (requirementKey: string, reason: string, answer: CaptureAnswer | undefined, clientMutationId: string) => ({ requirementKey, reason, expectedVersion: answer?.version ?? null, clientMutationId });
export const updateImpossibleAnswer = (answers: CaptureAnswer[], requirementKey: string, reason: string): CaptureAnswer[] => answers.some((answer) => answer.requirementKey === requirementKey)
  ? answers.map((answer) => answer.requirementKey === requirementKey && answer.impossibilityReason !== reason ? { ...answer, impossibilityReason: reason, version: answer.version + 1 } : answer)
  : [...answers, { requirementKey, mediaIds: [], impossibilityReason: reason, version: 1 }];
