export type PromotionState = "PREPARING" | "ACTIVATING" | "COMPLETED" | "FAILED" | "NEEDS_CONFIRMATION" | "CANCELED";

export function promotionProgressMessage(status?: PromotionState | null) {
  if (status === "PREPARING") return "Preparando o inventário…";
  if (status === "ACTIVATING") return "Ativando a nova origem…";
  return "";
}

export function shouldPollPromotion(status?: PromotionState | null) {
  return status === "PREPARING" || status === "ACTIVATING";
}

/** promotionJustCompleted is true only when the operation was watched finishing, not when an old result is loaded. */
export function promotionJustCompleted(previous: string | undefined | null, current: string | undefined | null) {
  return current === "COMPLETED" && !!previous && previous !== "COMPLETED";
}

export function promotionFailureAction(status: PromotionState | null | undefined, code?: string | null, retryable = false) {
  if (status === "NEEDS_CONFIRMATION") return "Confirmar substituição atualizada";
  if (status === "FAILED" && retryable) return "Tentar novamente";
  if (status === "FAILED" && (code === "EVIDENCE_UNAVAILABLE" || code === "EMPTY_INVENTORY")) return "Consultar evidências";
  return "";
}

export function preselectedOrigin(explicitChoice: boolean, activeCompatibleOriginId: string) {
  return explicitChoice ? "" : activeCompatibleOriginId;
}

export function scheduleReferenceVersionId(comparisonMode: string, followsActiveOrigin: boolean, selectedVersionId: string) {
  return comparisonMode === "FIXED_ORIGIN" && !followsActiveOrigin ? selectedVersionId : null;
}

export function promotionEligibility(eligible: boolean, generatedVersionActive: boolean, impedimentCount: number) {
  return {
    canPromote: eligible && !generatedVersionActive,
    showImpediments: impedimentCount > 0,
    alreadyActive: generatedVersionActive,
  };
}

export function canStartPromotion(eligible: boolean, generatedVersionActive: boolean, status?: PromotionState | null, retryable = false) {
  if (generatedVersionActive || !eligible) return false;
  if (status === "NEEDS_CONFIRMATION" || status == null || status === "CANCELED" || status === "COMPLETED") return true;
  return status === "FAILED" && !retryable;
}

export function replacementDraftDescription(source?: string | null, draftState?: string | null) {
  const sourceLabel: Record<string, string> = {
    CAPTURE: "captura de vistoria",
    COMPARATIVE_INSPECTION: "vistoria comparativa",
    LEGACY_ORIGIN: "catalogação da origem",
    CONTESTATION: "contestação",
    INSPECTION_PROMOTION: "promoção de vistoria",
  };
  const stateLabel: Record<string, string> = {
    PREPARING: "Em preparação",
    READY: "Pronto para revisão",
    FAILED: "Falhou",
  };
  return `O rascunho de inventário aberto para este imóvel (origem: ${sourceLabel[source ?? ""] ?? "origem desconhecida"}, estado: ${stateLabel[draftState ?? ""] ?? "estado desconhecido"}) será descartado.`;
}

export function promotionConfirmLabel(hasActiveOrigin: boolean, hasDraftToReplace: boolean) {
  return hasActiveOrigin || hasDraftToReplace ? "Confirmar substituição" : "Usar como origem";
}

export function promotionHistoricalMessage(versionNumber: number) {
  return `Esta vistoria gerou a versão ${versionNumber}, hoje substituída.`;
}

export function replacementDraftId(conflictingDraft?: { id: string; ownInspection: boolean } | null) {
  return conflictingDraft && !conflictingDraft.ownInspection ? conflictingDraft.id : null;
}

export function promotionCompletedMessage(versionNumber: number) {
  return `Versão ${versionNumber} criada e ativa.`;
}
