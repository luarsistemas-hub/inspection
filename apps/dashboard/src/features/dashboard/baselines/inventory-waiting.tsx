"use client";

import { useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { Button } from "@inspection/design-system";
import { graphql } from "@/graphql/client";
import * as G from "@/graphql/generated";
import { failureText, type BaselineVersion } from "./review-model";

const pollIntervalMs = 5000;
// After this many polls without a draft the page explains the delay instead of spinning silently.
const pollsBeforeDelayNotice = 24;

/** InventoryWaiting follows the draft created from one inspection until it is ready. */
export function InventoryWaiting({ inspectionId, canReview }: { inspectionId: string; canReview: boolean }) {
  const router = useRouter();
  const [assetId, setAssetId] = useState("");
  const [versions, setVersions] = useState<BaselineVersion[]>([]);
  const [polls, setPolls] = useState(0);
  const [error, setError] = useState("");
  useEffect(() => {
    if (!canReview) return;
    void graphql<G.InspectionDetailQuery, G.InspectionDetailQueryVariables>(G.InspectionDetailDocument, { id: inspectionId }).then((result) => {
      if (!result.inspection) throw new Error("A vistoria não está disponível neste contexto.");
      setAssetId(result.inspection.assetId);
    }).catch((failure) => setError(failureText(failure)));
  }, [inspectionId, canReview]);
  const refreshVersions = useCallback(async () => {
    if (!assetId) return;
    const result = await graphql<G.DashboardBaselineVersionsQuery, G.DashboardBaselineVersionsQueryVariables>(G.DashboardBaselineVersionsDocument, { assetId });
    setVersions(result.baselineVersions.nodes);
    setPolls((count) => count + 1);
  }, [assetId]);

  const fromInspection = versions.filter((version) => version.sourceInspectionId === inspectionId);
  const draft = fromInspection.find((version) => version.status === "DRAFT") ?? fromInspection[0];
  const state = draft?.draftState === "READY" || (draft && draft.status !== "DRAFT") ? "ready" : draft?.draftState === "FAILED" ? "failed" : "preparing";
  const settled = state !== "preparing";
  useEffect(() => {
    if (!assetId || settled) return;
    void refreshVersions().catch((failure) => setError(failureText(failure)));
    const timer = window.setInterval(() => { void refreshVersions().catch(() => undefined); }, pollIntervalMs);
    return () => window.clearInterval(timer);
  }, [assetId, refreshVersions, settled]);

  if (!canReview) return <section role="alert"><h2>Acesso restrito</h2><p>Somente operadores autorizados podem revisar um rascunho.</p></section>;
  const heading = state === "ready" ? "Inventário pronto" : state === "failed" ? "Não foi possível preparar o inventário" : "Preparando inventário";
  const message = state === "ready" ? "O inventário está pronto para revisão."
    : state === "failed" ? "A preparação falhou. Abra o rascunho para tentar novamente."
      : !draft && polls >= pollsBeforeDelayNotice ? "Ainda não há inventário para esta vistoria. A preparação pode levar alguns minutos; esta página continua verificando."
        : "A vistoria foi enviada e o inventário está sendo processado. Esta página atualiza o estado automaticamente.";
  return <section aria-labelledby="baseline-wait-heading">
    <h2 id="baseline-wait-heading">{heading}</h2>
    <p role="status">{message}</p>
    {draft && settled && <Button onClick={() => router.push(`/baselines/review/${draft.id}`)}>{state === "failed" ? "Abrir rascunho" : "Revisar inventário"}</Button>}
    {error && <p role="alert">{error}</p>}
  </section>;
}
