"use client";

import { useCallback, useEffect, useState } from "react";
import { Button, Field, Input, Select, Textarea } from "@inspection/design-system";
import { graphql, graphqlOptionalPermission, type GraphQLFailure } from "@/graphql/client";
import * as G from "@/graphql/generated";

const failureText = (failure: unknown) => failure instanceof Error ? failure.message : (failure as GraphQLFailure).message ?? "Não foi possível concluir a operação.";
const mutationId = () => `video-ops-${crypto.randomUUID()}`;

export function VideoOperationsPage() {
  const [statusFilter, setStatusFilter] = useState("");
  const [page, setPage] = useState<G.AdminVideoPreparationsQuery["videoPreparations"]>();
  const [admission, setAdmission] = useState<G.AdminVideoAdmissionQuery["videoAdmission"]>();
  const [admissionAllowed, setAdmissionAllowed] = useState(false);
  const [screeningBypassed, setScreeningBypassed] = useState(false);
  const [reason, setReason] = useState("");
  const [sourceMediaId, setSourceMediaId] = useState("");
  const [originVersionId, setOriginVersionId] = useState("");
  const [actionReason, setActionReason] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("Carregando preparações de vídeo…");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async (after?: string | null) => {
    try {
      const result = await graphql<G.AdminVideoPreparationsQuery>(G.AdminVideoPreparationsDocument, { status: statusFilter ? [statusFilter] : null, first: 25, after: after ?? null });
      setPage(result.videoPreparations);
      setNotice(`${result.videoPreparations.nodes.length} preparação(ões) carregada(s).`);
      setError("");
    } catch (failure) { setError(failureText(failure)); }
  }, [statusFilter]);

  useEffect(() => {
    void load();
    void graphqlOptionalPermission<G.AdminVideoAdmissionQuery>(G.AdminVideoAdmissionDocument).then((result) => { setAdmission(result.videoAdmission); setAdmissionAllowed(true); }).catch(() => { setAdmissionAllowed(false); });
    void graphql<G.AdminSensitiveContentScreeningStatusQuery>(G.AdminSensitiveContentScreeningStatusDocument).then((result) => setScreeningBypassed(result.sensitiveContentScreeningBypassed)).catch(() => setScreeningBypassed(false));
  }, [load]);

  const run = async (operation: () => Promise<void>) => {
    setBusy(true); setError("");
    try { await operation(); await load(); }
    catch (failure) { setError(failureText(failure)); }
    finally { setBusy(false); }
  };
  const retry = (row: G.AdminVideoPreparationsQuery["videoPreparations"]["nodes"][number]) => {
    if (!window.confirm(`Tentar novamente a preparação ${row.preparationId}?`)) return;
    void run(async () => { const r = await graphql<G.AdminRetryVideoPreparationMutation>(G.AdminRetryVideoPreparationDocument, { input: { mediaId: row.mediaId, expectedPreparationVersion: row.preparationVersion, clientMutationId: mutationId() } }); if (r.retryVideoPreparation.userErrors.length) throw new Error(r.retryVideoPreparation.userErrors[0].message); });
  };
  const reprocess = (row: G.AdminVideoPreparationsQuery["videoPreparations"]["nodes"][number]) => {
    const profileDigest = window.prompt("Digest do perfil implantado para reprocessar:");
    if (!profileDigest || !window.confirm(`Reprocessar a fonte ${row.mediaId} com o perfil informado?`)) return;
    void run(async () => { const r = await graphql<G.AdminReprocessVideoPreparationMutation>(G.AdminReprocessVideoPreparationDocument, { input: { mediaId: row.mediaId, expectedPreparationVersion: row.preparationVersion, profileDigest, clientMutationId: mutationId() } }); if (r.reprocessVideoPreparation.userErrors.length) throw new Error(r.reprocessVideoPreparation.userErrors[0].message); });
  };
  const send = async (field: "applyLegalHold" | "releaseLegalHold" | "recordDeletionRequest") => {
    const hasSource = Boolean(sourceMediaId.trim());
    const hasOrigin = Boolean(originVersionId.trim());
    if (!actionReason.trim() || hasSource === hasOrigin) {
      setError("Informe exatamente um ID: fonte ou versão de origem, além do motivo.");
      return;
    }
    await run(async () => {
      if (field === "applyLegalHold" || field === "releaseLegalHold") {
        const r = await graphql(field === "applyLegalHold" ? G.AdminApplyVideoLegalHoldDocument : G.AdminReleaseVideoLegalHoldDocument, { input: { sourceMediaId: sourceMediaId || null, originVersionId: originVersionId || null, reason: actionReason, clientMutationId: mutationId() } });
        const payload = field === "applyLegalHold" ? (r as G.AdminApplyVideoLegalHoldMutation).applyLegalHold : (r as G.AdminReleaseVideoLegalHoldMutation).releaseLegalHold;
        if (payload.userErrors.length) throw new Error(payload.userErrors[0].message);
      } else {
        const r = await graphql<G.AdminRecordVideoDeletionRequestMutation>(G.AdminRecordVideoDeletionRequestDocument, { input: { sourceMediaId: sourceMediaId || null, originVersionId: originVersionId || null, reason: actionReason, clientMutationId: mutationId() } });
        if (r.recordDeletionRequest.userErrors.length) throw new Error(r.recordDeletionRequest.userErrors[0].message);
      }
      setNotice("Operação registrada.");
    });
  };
  const saveAdmission = () => {
    if (!admission || !reason.trim()) { setError("Informe o motivo da alteração da admissão."); return; }
    void run(async () => { const r = await graphql<G.AdminConfigureVideoAdmissionMutation>(G.AdminConfigureVideoAdmissionDocument, { input: { enabled: !admission.enabled, expectedVersion: admission.version, reason, clientMutationId: mutationId() } }); if (r.configureVideoAdmission.userErrors.length || !r.configureVideoAdmission.admission) throw new Error(r.configureVideoAdmission.userErrors[0]?.message ?? "Não foi possível alterar a admissão."); setAdmission(r.configureVideoAdmission.admission); setReason(""); });
  };

  return <section aria-labelledby="video-operations-heading">
    <h2 id="video-operations-heading">Operações de vídeo</h2>
    <p role="status">{notice}</p>{error && <p role="alert">{error}</p>}
    {screeningBypassed && <p role="alert">Triagem de conteúdo sensível desativada.</p>}
    {admissionAllowed && admission && <section aria-label="Admissão de vídeo da plataforma"><h3>Admissão da plataforma</h3><p>Estado: {admission.enabled ? "Ativada" : "Desativada"} · versão {admission.version}</p><Field label="Motivo"><Input value={reason} onChange={(e) => setReason(e.target.value)} /></Field><Button disabled={busy || !reason.trim()} onClick={saveAdmission}>{admission.enabled ? "Desativar admissão" : "Ativar admissão"}</Button></section>}
    <h3>Preparações</h3><Field label="Filtrar por status"><Select value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)}><option value="">Todos</option>{["QUEUED", "RUNNING", "FAILED", "READY"].map((s) => <option key={s}>{s}</option>)}</Select></Field>
    <div>{page?.nodes.map((row) => <article key={row.preparationId} aria-label={`Preparação ${row.preparationId}`}><h4>{row.stage} · {row.status}</h4><p>Fonte {row.mediaId} · tentativas {row.attempts} · frames {row.processedFrames}</p><p>Erro seguro: {row.errorCode || "—"} · fonte {row.sourceAvailable ? "disponível" : "indisponível"}</p><Button disabled={busy || !row.retryable} onClick={() => retry(row)}>Tentar novamente</Button><Button disabled={busy} variant="secondary" onClick={() => reprocess(row)}>Reprocessar</Button></article>)}</div>
    {page?.pageInfo.hasNextPage && page.pageInfo.endCursor && <Button variant="secondary" onClick={() => void load(page.pageInfo.endCursor)}>Próxima página</Button>}
    <h3>Retenção e exclusão</h3><p>Para retenção e exclusão, informe somente um alvo: fonte ou versão de origem.</p><Field label="ID da fonte"><Input value={sourceMediaId} onChange={(e) => setSourceMediaId(e.target.value)} /></Field><Field label="ID da versão de origem"><Input value={originVersionId} onChange={(e) => setOriginVersionId(e.target.value)} /></Field><Field label="Motivo"><Textarea value={actionReason} onChange={(e) => setActionReason(e.target.value)} /></Field>
    <div className="admin-action-row"><Button disabled={busy} onClick={() => void send("applyLegalHold")}>Aplicar retenção</Button><Button variant="secondary" disabled={busy} onClick={() => void send("releaseLegalHold")}>Liberar retenção</Button><Button variant="secondary" disabled={busy} onClick={() => { if (window.confirm("Registrar solicitação de exclusão para o escopo informado?")) void send("recordDeletionRequest"); }}>Solicitar exclusão</Button></div>
    <p>A revisão de privacidade é feita no Dashboard, na fila de Pendências.</p><a href={`${process.env.NEXT_PUBLIC_DASHBOARD_URL ?? "http://localhost:3002"}/inspections?status=pendencias`}>Abrir o Dashboard</a>
  </section>;
}
