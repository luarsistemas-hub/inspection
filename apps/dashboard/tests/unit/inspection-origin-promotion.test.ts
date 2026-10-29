import { describe, expect, it } from "vitest";
import { canStartPromotion, preselectedOrigin, promotionCompletedMessage, promotionConfirmLabel, promotionEligibility, promotionFailureAction, promotionHistoricalMessage, promotionJustCompleted, promotionProgressMessage, replacementDraftDescription, replacementDraftId, scheduleReferenceVersionId, shouldPollPromotion } from "../../src/features/dashboard/inspection-origin-promotion-model";

describe("inspection origin promotion model", () => {
  it("shows eligibility blockers and progress polling states", () => {
    expect(promotionEligibility(true, false, 0)).toEqual({ canPromote: true, showImpediments: false, alreadyActive: false });
    expect(promotionEligibility(false, false, 2)).toEqual({ canPromote: false, showImpediments: true, alreadyActive: false });
    expect(promotionEligibility(false, true, 0).alreadyActive).toBe(true);
    expect(promotionProgressMessage("PREPARING")).toBe("Preparando o inventário…");
    expect(promotionProgressMessage("ACTIVATING")).toBe("Ativando a nova origem…");
    expect(shouldPollPromotion("PREPARING")).toBe(true);
    expect(shouldPollPromotion("COMPLETED")).toBe(false);
    expect(promotionJustCompleted("ACTIVATING", "COMPLETED")).toBe(true);
    expect(promotionJustCompleted("FAILED", "COMPLETED")).toBe(true);
    expect(promotionJustCompleted(undefined, "COMPLETED")).toBe(false);
    expect(promotionJustCompleted("COMPLETED", "COMPLETED")).toBe(false);
    expect(promotionJustCompleted("PREPARING", "FAILED")).toBe(false);
  });

  it("provides recovery actions for failures and changed confirmations", () => {
    expect(promotionFailureAction("FAILED", "CATALOG_FAILED", true)).toBe("Tentar novamente");
    expect(promotionFailureAction("FAILED", "EVIDENCE_UNAVAILABLE")).toBe("Consultar evidências");
    expect(promotionFailureAction("NEEDS_CONFIRMATION")).toBe("Confirmar substituição atualizada");
  });

  it("marks only another inspection's open draft for replacement and reports completion", () => {
    expect(replacementDraftId({ id: "other-draft", ownInspection: false })).toBe("other-draft");
    expect(replacementDraftId({ id: "own-draft", ownInspection: true })).toBeNull();
    expect(promotionCompletedMessage(4)).toBe("Versão 4 criada e ativa.");
    expect(promotionHistoricalMessage(4)).toBe("Esta vistoria gerou a versão 4, hoje substituída.");
  });

  it("describes a replaced draft in Portuguese without exposing its identifier", () => {
    expect(replacementDraftDescription("CAPTURE", "PREPARING")).toBe("O rascunho de inventário aberto para este imóvel (origem: captura de vistoria, estado: Em preparação) será descartado.");
    expect(replacementDraftDescription("COMPARATIVE_INSPECTION", "READY")).toContain("vistoria comparativa, estado: Pronto para revisão");
    expect(replacementDraftDescription("LEGACY_ORIGIN", "FAILED")).toContain("catalogação da origem, estado: Falhou");
    expect(replacementDraftDescription("CONTESTATION", null)).toContain("contestação, estado: estado desconhecido");
    expect(replacementDraftDescription("INSPECTION_PROMOTION", "READY")).toContain("promoção de vistoria");
  });

  it("uses the replacement label only when an active origin or another draft will be replaced", () => {
    expect(promotionConfirmLabel(true, false)).toBe("Confirmar substituição");
    expect(promotionConfirmLabel(false, true)).toBe("Confirmar substituição");
    expect(promotionConfirmLabel(false, false)).toBe("Usar como origem");
  });

  it("allows a new promotion after cancellation, a non-retryable failure or a superseded version", () => {
    expect(canStartPromotion(true, false, "CANCELED")).toBe(true);
    expect(canStartPromotion(true, false, "FAILED", false)).toBe(true);
    expect(canStartPromotion(true, false, "COMPLETED")).toBe(true);
    expect(canStartPromotion(true, true, "COMPLETED")).toBe(false);
    expect(canStartPromotion(true, false, "FAILED", true)).toBe(false);
    expect(canStartPromotion(true, false, "PREPARING")).toBe(false);
  });

  it("preserves explicit choices and pins only compatible automatic choices", () => {
    expect(preselectedOrigin(false, "active-version")).toBe("active-version");
    expect(preselectedOrigin(true, "active-version")).toBe("");
  });

  it("follows the active origin by sending a null version by default", () => {
    expect(scheduleReferenceVersionId("FIXED_ORIGIN", true, "old-version")).toBeNull();
    expect(scheduleReferenceVersionId("FIXED_ORIGIN", false, "pinned-version")).toBe("pinned-version");
    expect(scheduleReferenceVersionId("CHECKLIST_ONLY", false, "pinned-version")).toBeNull();
  });
});
