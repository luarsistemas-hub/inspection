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

  it("flags partial delivery, exhausted retries, and unknown outcomes", () => {
    expect(deliveryPresentation("SENT", [{ status: "DELIVERED", attempts: 1 }, { status: "FAILED", attempts: 4 }]).partial).toBe(true);
    expect(deliveryPresentation("FAILED", [{ status: "FAILED", attempts: 4 }]).retryExhausted).toBe(true);
    expect(deliveryPresentation("UNKNOWN").needsReview).toBe(true);
  });
});
