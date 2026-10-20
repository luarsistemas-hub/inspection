import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { graphql } = vi.hoisted(() => ({ graphql: vi.fn() }));
vi.mock("@/graphql/client", () => ({ graphql }));
vi.mock("next/navigation", () => ({ useSearchParams: () => new URLSearchParams() }));
vi.mock("@inspection/design-system", () => ({ Dialog: ({ isOpen, children }: { isOpen: boolean; children: unknown }) => isOpen ? createElement("div", { role: "dialog" }, children as never) : null }));

import { PrivacyReviewJourney } from "@/features/dashboard/privacy-review-journey";

const item = { mediaId: "media-1", inspectionId: "inspection-1", assetName: "Imóvel QA", participantName: "Participante QA", mediaKind: "PHOTO", requirementKey: "overview", blockedAt: "2026-10-01T10:00:00Z", submitted: false, sourceAvailable: true };

describe("PrivacyReviewJourney", () => {
  beforeEach(() => { graphql.mockReset(); });

  it("loads the privacy queue and opens the requested short lived original", async () => {
    graphql.mockResolvedValueOnce({ sensitiveMediaReviews: { nodes: [item], pageInfo: { endCursor: null, hasNextPage: false } } });
    graphql.mockResolvedValueOnce({ sensitiveMediaOriginal: { mediaId: "media-1", mediaKind: "PHOTO", url: "https://signed.invalid/short", expiresAt: "2026-10-01T10:05:00Z" } });
    const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => { root.render(createElement(PrivacyReviewJourney)); });
    expect(host.textContent).toContain("Imóvel QA");
    const view = [...host.querySelectorAll("button")].find((button) => button.textContent === "Ver original");
    await act(async () => { view?.dispatchEvent(new MouseEvent("click", { bubbles: true })); });
    expect(host.querySelector("img")?.getAttribute("src")).toBe("https://signed.invalid/short");
    expect(graphql).toHaveBeenCalledTimes(2);
    await act(async () => { root.unmount(); });
  });

  it("requires a reason and submits the false positive decision", async () => {
    graphql.mockResolvedValueOnce({ sensitiveMediaReviews: { nodes: [item], pageInfo: { endCursor: null, hasNextPage: false } } });
    graphql.mockResolvedValueOnce({ declareSensitiveDetectionFalsePositive: { userErrors: [], clientMutationId: "mutation-1" } });
    graphql.mockResolvedValueOnce({ sensitiveMediaReviews: { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } } });
    const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => { root.render(createElement(PrivacyReviewJourney)); });
    const button = [...host.querySelectorAll("button")].find((element) => element.textContent?.includes("Liberar mídia")) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    const reason = host.querySelector("textarea") as HTMLTextAreaElement;
    await act(async () => { const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set; setter?.call(reason, "Revisão confirmou falso positivo"); reason.dispatchEvent(new Event("input", { bubbles: true })); reason.dispatchEvent(new Event("change", { bubbles: true })); });
    expect(button.disabled).toBe(false);
    await act(async () => { button.dispatchEvent(new MouseEvent("click", { bubbles: true })); });
    expect(graphql).toHaveBeenCalledTimes(3);
    await act(async () => { root.unmount(); });
  });
});
