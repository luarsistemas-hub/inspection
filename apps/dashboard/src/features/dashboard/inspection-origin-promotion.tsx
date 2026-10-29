"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { graphql } from "@/graphql/client";
import { InspectionOriginPromotionDocument, PromoteInspectionToOriginDocument, type InspectionOriginPromotionQuery } from "@/graphql/generated";
import { ConfirmationDialog } from "./confirmation-dialog";
import { purposeOptions, purposeLabel } from "./inventory-presentation";
import { newClientMutationId } from "./baselines/review-model";
import { canStartPromotion, promotionCompletedMessage, promotionConfirmLabel, promotionEligibility, promotionHistoricalMessage, promotionJustCompleted, promotionProgressMessage, replacementDraftDescription, replacementDraftId, shouldPollPromotion } from "./inspection-origin-promotion-model";

type PromotionStatus = InspectionOriginPromotionQuery["inspectionOriginPromotion"];

/** InspectionOriginPromotion provides one confirmation and recovery flow for every inspection media kind. */
export function InspectionOriginPromotion({ inspectionId, assetId, inspectionLabel, onCompleted }: { inspectionId: string; assetId: string; inspectionLabel: string; onCompleted?: () => void }) {
  const router = useRouter();
  const [status, setStatus] = useState<PromotionStatus>();
  const [message, setMessage] = useState("Verificando se esta vistoria pode ser usada como origem…");
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [purpose, setPurpose] = useState("INVENTORY_UPDATE");
  const requestId = useRef("");
  const loading = useRef(false);

  const reload = useCallback(async () => {
    if (loading.current) return;
    loading.current = true;
    try {
      const result = await graphql<InspectionOriginPromotionQuery, { inspectionId: string }>(InspectionOriginPromotionDocument, { inspectionId });
      setStatus(result.inspectionOriginPromotion);
      setMessage("");
      return result.inspectionOriginPromotion;
    } catch {
      setMessage("Não foi possível consultar a promoção da origem. Tente novamente.");
      return undefined;
    } finally { loading.current = false; }
  }, [inspectionId]);

  useEffect(() => { void reload(); }, [reload]);
  const operationId = status?.currentPromotion?.id;
  const operationStatus = status?.currentPromotion?.status;
  const previous = useRef<{ id?: string; status?: string }>({});
  const requested = useRef(false);
  // Tells the parent only when this session watched the operation finish, so a page that loads an old result does not refetch.
  // A request made here also counts: its operation may already be COMPLETED on the first reload.
  useEffect(() => {
    const finishedHere = requested.current && operationStatus === "COMPLETED" && operationId !== previous.current.id;
    if (finishedHere || (operationId === previous.current.id && promotionJustCompleted(previous.current.status, operationStatus))) {
      requested.current = false;
      onCompleted?.();
    }
    previous.current = { id: operationId, status: operationStatus };
  }, [operationId, operationStatus, onCompleted]);
  useEffect(() => {
    if (!shouldPollPromotion(operationStatus)) return;
    const timer = window.setInterval(() => { void reload(); }, 3000);
    return () => window.clearInterval(timer);
  }, [operationStatus, reload]);

  const request = async () => {
    if (!status?.confirmationDigest) throw new Error("Atualize os dados da vistoria antes de confirmar.");
    const conflictingDraft = status.conflictingDraft;
    let result;
    try {
      result = await graphql(PromoteInspectionToOriginDocument, { input: {
        inspectionId,
        clientMutationId: requestId.current || (requestId.current = newClientMutationId()),
        purpose,
        replaceDraftId: replacementDraftId(conflictingDraft),
        confirmationDigest: status.confirmationDigest,
      } });
    } catch (failure) {
      throw new Error("Não foi possível confirmar agora. Tente novamente.");
    }
    const error = result.promoteInspectionToOrigin.userErrors[0];
    if (error) {
      if (["CONFIRMATION_DIGEST_CHANGED", "REPLACE_DRAFT_CHANGED", "NEEDS_CONFIRMATION"].includes(error.code) || ["confirmationDigest", "replaceDraftId"].includes(error.field ?? "")) {
        const latest = await reload();
        if (latest) setConfirmOpen(true);
      }
      throw new Error(error.message || "Não foi possível confirmar o uso desta vistoria como origem.");
    }
    requested.current = true;
    await reload();
  };

  const retry = async () => {
    requestId.current = newClientMutationId();
    await request();
  };
  const current = status?.currentPromotion;
  const activeVersionId = current?.originVersion?.id;
  const completed = current?.status === "COMPLETED" && current.originVersion;
  const completedIsHistorical = Boolean(completed && !status?.generatedVersionActive);
  const evidenceFailure = current?.status === "FAILED" && ["EVIDENCE_UNAVAILABLE", "EMPTY_INVENTORY"].includes(current.failureCode ?? "");
  const eligibility = promotionEligibility(status?.eligible ?? false, status?.generatedVersionActive ?? false, status?.impediments.length ?? 0);

  return <section className="origin-promotion" aria-label="Promoção da vistoria para origem">
    {message && <p role="status" aria-live="polite">{message}</p>}
    {status?.generatedVersionActive && <p role="status">Esta vistoria já é a origem ativa.</p>}
    {status?.automaticCatalogSkipped && <p role="status">A catalogação automática desta vistoria foi ignorada porque havia outro rascunho aberto.</p>}
    {status?.impediments.map((item) => <p key={`${item.code}:${item.message}`} className="warning">{item.message}</p>)}
    {shouldPollPromotion(operationStatus) && <p role="status" aria-live="polite">{promotionProgressMessage(operationStatus)}</p>}
    {current?.status === "FAILED" && <p role="alert">{current.failureReason || "Não foi possível usar esta vistoria como origem."}</p>}
    {evidenceFailure && <a href={`/reports?inspectionId=${encodeURIComponent(inspectionId)}`}>Consultar evidências</a>}
    {current?.status === "FAILED" && current.retryable && <button type="button" onClick={() => void retry().catch((failure: unknown) => setMessage(failure instanceof Error ? failure.message : "Não foi possível repetir a promoção."))}>Tentar novamente</button>}
    {completed && <><p role="status">{completedIsHistorical ? promotionHistoricalMessage(completed.versionNumber) : promotionCompletedMessage(completed.versionNumber)}</p>
      <button type="button" className="secondary" onClick={() => router.push(`/baselines/review/${encodeURIComponent(activeVersionId!)}`)}>Ver origem</button>
      {!completedIsHistorical && <button type="button" className="secondary" onClick={() => router.push(`/inspections?create=1&assetId=${encodeURIComponent(assetId)}&originVersionId=${encodeURIComponent(activeVersionId!)}`)}>Criar vistoria com esta origem</button>}
    </>}
    {!eligibility.alreadyActive && (current?.status === "NEEDS_CONFIRMATION" || canStartPromotion(status?.eligible ?? false, status?.generatedVersionActive ?? false, operationStatus, current?.retryable ?? false)) && <button type="button" onClick={() => { requestId.current = ""; if (current?.status === "NEEDS_CONFIRMATION") void reload().then((latest) => { if (latest) setConfirmOpen(true); }); else setConfirmOpen(true); }}>{current?.status === "NEEDS_CONFIRMATION" ? "Confirmar substituição atualizada" : "Usar esta vistoria como origem"}</button>}
    <ConfirmationDialog isOpen={confirmOpen} onClose={() => setConfirmOpen(false)} title="Usar esta vistoria como origem" target={inspectionLabel} scope="Inventário do imóvel" consequence="Serão usados somente os ambientes, itens, condições e evidências observados nesta vistoria. Dados exclusivos da origem anterior permanecem no histórico." confirmLabel={promotionConfirmLabel(Boolean(status?.activeOrigin), Boolean(status?.conflictingDraft && !status.conflictingDraft.ownInspection))} onConfirm={request}>
      <p>{status?.activeOrigin ? `Origem que será substituída: versão ${status.activeOrigin.versionNumber}.` : "Nenhuma origem ativa."}</p>
      {status?.conflictingDraft && !status.conflictingDraft.ownInspection && <p>{replacementDraftDescription(status.conflictingDraft.source, status.conflictingDraft.draftState)}</p>}
      <label>Finalidade<select aria-label="Finalidade do inventário" value={purpose} onChange={(event) => setPurpose(event.target.value)}>{purposeOptions.map(([key]) => <option key={key} value={key}>{purposeLabel(key)}</option>)}</select></label>
    </ConfirmationDialog>
  </section>;
}
