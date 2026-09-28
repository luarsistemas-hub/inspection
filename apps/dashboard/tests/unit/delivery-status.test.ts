import { describe, expect, it } from "vitest";
import { deliveryPresentation } from "../../src/features/notifications/delivery-status";

describe("deliveryPresentation", () => {
  it("maps every durable state without treating queued or accepted as delivered", () => {
    expect(Object.fromEntries(["QUEUED", "PROCESSING", "ACCEPTED", "SENT", "DELIVERED", "FAILED", "UNKNOWN", "CANCELED"].map((state) => [state, deliveryPresentation(state).label]))).toEqual({
      QUEUED: "Solicitada",
      PROCESSING: "Em processamento",
      ACCEPTED: "Aceita pelo provedor",
      SENT: "Enviada",
      DELIVERED: "Entregue",
      FAILED: "Falhou",
      UNKNOWN: "Revisão necessária",
      CANCELED: "Cancelada",
    });
  });

  it("UT-056 flags partial and exhausted delivery for review without claiming success", () => {
    const partial = deliveryPresentation("SENT", [{ status: "DELIVERED", attempts: 1 }, { status: "FAILED", attempts: 4 }]);
    const exhausted = deliveryPresentation("FAILED", [{ status: "FAILED", attempts: 4 }]);
    expect(partial).toMatchObject({ label: "Enviada", needsReview: true, partial: true });
    expect(exhausted).toMatchObject({ label: "Falhou", needsReview: true, retryExhausted: true });
    expect(deliveryPresentation("UNKNOWN").needsReview).toBe(true);
  });
});
