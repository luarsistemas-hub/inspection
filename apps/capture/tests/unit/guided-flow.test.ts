import { describe, expect, it } from "vitest";
import type { ExternalCaptureBootstrapQuery } from "@/graphql/generated";
import type { CaptureDraft } from "@/pwa/drafts";
import { guidedProgress, guidedRequirements, nextGuidedRequirement, requirementState } from "@/pwa/guided-flow";

type Requirement = ExternalCaptureBootstrapQuery["externalCapture"]["requirements"][number];
type Answer = ExternalCaptureBootstrapQuery["externalCapture"]["answers"][number];
const requirement = (key: string): Requirement => ({ key, section: "Referência", label: "Referência", instructions: key, required: true, minimumMedia: 1, maximumMedia: 1, descriptionRequired: false, captureSourcePolicy: "CAMERA_DEFAULT", comparisonTarget: "FIXED_ORIGIN", impossibilityAllowed: true });
const answer = (key: string, mediaIds: string[], impossibilityReason: string | null = null): Answer => ({ requirementKey: key, mediaIds, impossibilityReason, version: 1 });
const draft = (key: string, mediaId: string | undefined, mediaStatus: string | undefined, metadataSaved: boolean): CaptureDraft => ({ metadata: { requirementKey: key }, mediaId, mediaStatus, metadataSaved }) as CaptureDraft;

describe("guided comparison flow", () => {
  const kitchen = requirement("origin:kitchen");
  const bedroom = requirement("origin:bedroom");

  it("includes only pinned fixed-origin requirements, in server order", () => {
    expect(guidedRequirements([{ ...kitchen, key: "checklist" }, kitchen, { ...bedroom, comparisonTarget: "CHECKLIST_ONLY" }, bedroom])).toEqual([kitchen, bedroom]);
  });

  it("advances to the next missing photo and counts uploaded or justified steps", () => {
    const requirements = [kitchen, bedroom];
    const answers = [answer(kitchen.key, ["photo-1"])];
    expect(requirementState(kitchen, answers, [])).toBe("complete");
    expect(nextGuidedRequirement(requirements, kitchen.key, answers, [])).toBe(bedroom.key);
    expect(guidedProgress(requirements, answers, [])).toBe(1);
    expect(guidedProgress(requirements, [...answers, answer(bedroom.key, [], "Cômodo inacessível")], [])).toBe(2);
  });

  it("does not count a pending or blocked local upload as complete", () => {
    const answers = [answer(kitchen.key, ["photo-1"])];
    expect(requirementState(kitchen, answers, [draft(kitchen.key, "photo-1", undefined, false)])).toBe("pending");
    expect(requirementState(kitchen, answers, [draft(kitchen.key, "photo-1", "SCREENED", true)])).toBe("blocked");
    expect(nextGuidedRequirement([kitchen, bedroom], bedroom.key, answers, [draft(kitchen.key, "photo-1", "SCREENED", true)])).toBeUndefined();
  });
});
