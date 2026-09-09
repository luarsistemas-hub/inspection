import { describe, expect, it } from "vitest";
import { buildImpossibilityInput, mergeUploadedAnswer, updateImpossibleAnswer } from "@/pwa/capture-policy";

const answer = (mediaIds: string[], version = 2) => ({ requirementKey: "front", mediaIds, impossibilityReason: null, version });

describe("capture page answer state", () => {
  it("preserves existing media when an additional upload succeeds", () => {
    expect(mergeUploadedAnswer([answer(["media-1"])], "front", "media-2", false)).toEqual([answer(["media-1", "media-2"], 3)]);
  });

  it("removes only the blocked media and preserves the other answer media", () => {
    expect(mergeUploadedAnswer([answer(["media-1", "media-2"])], "front", "media-2", true)).toEqual([answer(["media-1"])]);
  });

  it("does not create a local answer for a blocked upload", () => {
    expect(mergeUploadedAnswer([], "front", "screened-media", true)).toEqual([]);
  });

  it("uses the current answer version for impossibility and preserves its media", () => {
    const current = answer(["media-1"]);
    expect(buildImpossibilityInput("front", "inacessível", current, "mutation-1")).toEqual({ requirementKey: "front", reason: "inacessível", expectedVersion: 2, clientMutationId: "mutation-1" });
    expect(updateImpossibleAnswer([current], "front", "inacessível")).toEqual([{ ...current, impossibilityReason: "inacessível", version: 3 }]);
  });

  it("keeps an idempotent impossibility at the same version", () => {
    const current = { ...answer([]), impossibilityReason: "inacessível" };
    expect(updateImpossibleAnswer([current], "front", "inacessível")).toEqual([current]);
  });
});
