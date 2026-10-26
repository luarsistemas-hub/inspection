import { describe, expect, it } from "vitest";
import { counterpartyOutcome, evidenceCaption } from "@/pwa/counterparty-presentation";

describe("counterparty outcome", () => {
  it("has no outcome while the response is pending", () => {
    expect(counterpartyOutcome({ response: "PENDING", resolution: "OPEN" })).toBeNull();
  });

  it("reports acknowledgment and a contestation under review", () => {
    expect(counterpartyOutcome({ response: "ACKNOWLEDGED", resolution: "OPEN" })?.message).toContain("confirmou ciência");
    expect(counterpartyOutcome({ response: "CONTESTED", resolution: "OPEN" })?.message).toContain("aguarda análise");
  });

  it("reports declined and accepted contestations with the adjustment state", () => {
    expect(counterpartyOutcome({ response: "CONTESTED", resolution: "DECLINED" })).toEqual({ tone: "warning", message: "Sua manifestação foi analisada e não foi acolhida. A vistoria de referência foi mantida." });
    expect(counterpartyOutcome({ response: "CONTESTED", resolution: "ACCEPTED", adjustmentStatus: "DRAFT" })?.message).toContain("em preparação");
    expect(counterpartyOutcome({ response: "CONTESTED", resolution: "ACCEPTED", adjustmentStatus: "ACTIVE" })?.message).toContain("foi publicada");
    expect(counterpartyOutcome({ response: "CONTESTED", resolution: "ACCEPTED", adjustmentStatus: "DISCARDED" })?.message).toBe("Sua manifestação foi acolhida, sem nova versão da vistoria.");
  });
});

describe("evidence caption", () => {
  it("numbers photos and shows the video moment of frames", () => {
    expect(evidenceCaption({ kind: "PHOTO" }, 0)).toBe("Foto 1");
    expect(evidenceCaption({ kind: "VIDEO_FRAME", timestampMs: 75_400 }, 1)).toBe("Quadro do vídeo · 1:15");
    expect(evidenceCaption({ kind: "VIDEO_FRAME", timestampMs: null }, 2)).toBe("Quadro do vídeo");
  });
});
