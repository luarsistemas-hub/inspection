"use client";

import { FormEvent, useCallback, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, Field, Input, Select } from "@inspection/design-system";
import { graphql } from "@/graphql/client";
import * as G from "@/graphql/generated";
import { ConfirmationDialog } from "../confirmation-dialog";
import { FormDialog } from "../form-dialog";
import { conditionLabel, conditionOptions, environmentTypeLabel, itemTypeLabel, itemTypeOptions, purposeOptions } from "../inventory-presentation";
import { EvidenceStrip } from "./evidence-strip";
import { AudioNoteReview } from "./audio-note-review";
import { itemAttributes, ReadOnlyInventory } from "./read-only-inventory";
import { EditItemForm, EnvironmentForm, MergeItemsForm, RejectItemForm, SplitItemForm } from "./review-forms";
import { buildActivationSummary, buildReviewGroups, decisionLabel, failureText, formatEvidenceTime, isExpectedVersionConflict, isPreparingDraft, newClientMutationId, throwUserErrors, type InventoryDraft, type ReviewEnvironment, type ReviewItem } from "./review-model";
import { shouldPollInspectionNotes } from "../voice-notes-model";

const bulkMinimumConfidenceBps = 8500;
const emptyVoiceSummary = { pendingVoiceItems: 0, conflictItems: 0, staleSuggestions: 0, notesWithoutItem: 0 };
type Participant = G.DashboardCounterpartyOptionsQuery["participants"]["nodes"][number];

/** InventoryReview is the internal review of one baseline draft. */
export function InventoryReview({ reviewId }: { reviewId: string }) {
  const router = useRouter();
  const [draft, setDraft] = useState<InventoryDraft>();
  const [businessUnitId, setBusinessUnitId] = useState<string>();
  const [status, setStatus] = useState("Carregando inventários…");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [reloadConflict, setReloadConflict] = useState(false);
  const [purpose, setPurpose] = useState("");
  const [participants, setParticipants] = useState<Participant[]>([]);
  const [participantsAfter, setParticipantsAfter] = useState<string | null>(null);
  const [participantsHaveMore, setParticipantsHaveMore] = useState(false);
  const [activationParticipantIds, setActivationParticipantIds] = useState<string[]>([]);
  const [selectedReviewItemId, setSelectedReviewItemId] = useState<string>();
  const [selectedReviewEnvironmentId, setSelectedReviewEnvironmentId] = useState<string>();
  const [rejectingItem, setRejectingItem] = useState<ReviewItem>();
  const [editingItem, setEditingItem] = useState<ReviewItem>();
  const [mergingItem, setMergingItem] = useState<ReviewItem>();
  const [splittingItem, setSplittingItem] = useState<ReviewItem>();
  const [editingEnvironment, setEditingEnvironment] = useState<ReviewEnvironment | null | undefined>();
  const [removingEnvironment, setRemovingEnvironment] = useState<ReviewEnvironment>();
  const [bulkEnvironment, setBulkEnvironment] = useState<ReviewEnvironment>();
  const [confirmActivation, setConfirmActivation] = useState(false);
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  const [inspectionNotes, setInspectionNotes] = useState<G.DashboardAudioNotesQuery["audioNotes"]["nodes"]>([]);
  const [selectedLateNoteIds, setSelectedLateNoteIds] = useState<string[]>([]);

  const refreshDraft = useCallback(async (updateStatus = true) => {
    const result = await graphql<G.DashboardInventoryDraftQuery, G.DashboardInventoryDraftQueryVariables>(G.DashboardInventoryDraftDocument, { originVersionId: reviewId });
    const next = result.inventoryDraft ?? undefined;
    setDraft(next);
    if (!updateStatus) return;
    if (!next) setStatus("Este inventário não está disponível neste contexto.");
    else if (isPreparingDraft(next)) setStatus("O inventário está sendo preparado.");
    else {
      const { decided, pending } = next.progress;
      setStatus(`${decided} ${decided === 1 ? "item revisado" : "itens revisados"} · ${pending} ${pending === 1 ? "pendente" : "pendentes"}.`);
    }
  }, [reviewId]);
  const loadParticipants = useCallback(async (after: string | null) => {
    const result = await graphql<G.DashboardCounterpartyOptionsQuery, G.DashboardCounterpartyOptionsQueryVariables>(G.DashboardCounterpartyOptionsDocument, { after });
    const active = result.participants.nodes.filter((participant) => participant.status === "ACTIVE");
    setParticipants((current) => after ? [...current, ...active] : active);
    setParticipantsAfter(result.participants.pageInfo.endCursor ?? null);
    setParticipantsHaveMore(result.participants.pageInfo.hasNextPage);
  }, []);
  useEffect(() => { void refreshDraft().catch((failure) => setError(failureText(failure))); }, [refreshDraft]);
  useEffect(() => {
    if (!draft?.warnings.includes("REGENERATION_RUNNING")) return;
    const timer = window.setInterval(() => { void refreshDraft().catch(() => undefined); }, 2500);
    return () => window.clearInterval(timer);
  }, [draft?.warnings, refreshDraft]);
  useEffect(() => {
    const inspectionId = draft?.sourceInspectionId;
    if (!inspectionId) { setInspectionNotes([]); return; }
    let active = true;
    let loadingNotes = false;
    let timer: number | undefined;
    const regenerationRunning = Boolean(draft?.warnings.includes("REGENERATION_RUNNING"));
    const loadNotes = async () => {
      if (loadingNotes) return;
      loadingNotes = true;
      try {
        const result = await graphql<G.DashboardAudioNotesQuery, G.DashboardAudioNotesQueryVariables>(G.DashboardAudioNotesDocument, { inspectionId, environmentId: "", first: 100, after: null });
        if (active) {
          setInspectionNotes(result.audioNotes.nodes);
          if (!shouldPollInspectionNotes(result.audioNotes.nodes, regenerationRunning) && timer !== undefined) window.clearInterval(timer);
        }
      } catch { /* Keep the last successful list available during transient failures. */ }
      finally { loadingNotes = false; }
    };
    void loadNotes();
    timer = window.setInterval(() => { void loadNotes(); }, 3000);
    return () => { active = false; window.clearInterval(timer); };
  }, [draft?.sourceInspectionId, draft?.warnings.includes("REGENERATION_RUNNING")]);
  useEffect(() => { void loadParticipants(null).catch((failure) => setError(failureText(failure))); }, [loadParticipants]);
  // Counterparties are offered from the draft's own asset unit.
  const assetId = draft?.assetId;
  useEffect(() => {
    if (!assetId) return;
    void graphql<G.DashboardBaselineAssetQuery, G.DashboardBaselineAssetQueryVariables>(G.DashboardBaselineAssetDocument, { id: assetId })
      .then((result) => setBusinessUnitId(result.asset?.businessUnitId))
      .catch((failure) => setError(failureText(failure)));
  }, [assetId]);

  const lastFailure = useRef("");
  // run reports whether the action succeeded so dialogs stay open on failure.
  const run = async (action: () => Promise<void>, refresh = true) => {
    if (busy) { lastFailure.current = "Aguarde a operação em andamento."; return false; }
    setBusy(true);
    setError("");
    try {
      await action();
      setReloadConflict(false);
      if (refresh) await refreshDraft();
      return true;
    } catch (failure) {
      const conflict = isExpectedVersionConflict(failure);
      lastFailure.current = conflict ? "Este rascunho foi atualizado por outra pessoa. Atualize antes de tentar novamente." : failureText(failure);
      setReloadConflict(conflict);
      setError(lastFailure.current);
      return false;
    } finally {
      setBusy(false);
    }
  };
  // openDialog clears a page error so it is not shown inside the new dialog.
  const openDialog = (open: () => void) => { setError(""); open(); };
  // mustSucceed keeps a confirmation dialog open with the failure message.
  const mustSucceed = (succeeded: boolean) => { if (!succeeded) throw new Error(lastFailure.current || "Não foi possível concluir a operação."); };
  const decide = (item: ReviewItem, action: string, patch: G.InventoryItemPatchInput | null = null, reason: string | null = null, reasonText: string | null = null) => run(async () => {
    const result = await graphql<G.DecideInventoryItemsMutation, G.DecideInventoryItemsMutationVariables>(G.DecideInventoryItemsDocument, { input: { originVersionId: reviewId, decisions: [{ itemId: item.id, action, expectedVersion: item.version, patch, reason, reasonText }], clientMutationId: newClientMutationId() } });
    throwUserErrors(result.decideInventoryItems.userErrors);
  });
  const addDeclaredItem = (event: FormEvent<HTMLFormElement>, environmentId: string) => {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    const chosen = new Set(form.getAll("evidenceIds").map(String));
    const evidence = (draft?.environments.flatMap((environment) => environment.items.flatMap((item) => item.evidence)) ?? [])
      .filter((candidate) => chosen.has(candidate.id))
      .map(({ kind, mediaId, frameId }) => ({ kind, mediaId, frameId, region: null }));
    void run(async () => {
      const result = await graphql<G.AddInventoryItemMutation, G.AddInventoryItemMutationVariables>(G.AddInventoryItemDocument, { input: { originVersionId: reviewId, environmentId, typeCode: String(form.get("typeCode") ?? "OTHER"), name: String(form.get("name") ?? "").trim(), condition: String(form.get("condition") ?? "GOOD"), material: null, color: null, quantity: null, observation: "OBSERVED", limitation: null, evidence, clientMutationId: newClientMutationId() } });
      throwUserErrors(result.addInventoryItem.userErrors);
      formElement.reset();
    });
  };
  const activate = async () => {
    if (!purpose || !draft) { lastFailure.current = "Escolha o propósito desta versão."; setError(lastFailure.current); return false; }
    let activated = "";
    await run(async () => {
      const result = await graphql<G.ActivateBaselineVersionMutation, G.ActivateBaselineVersionMutationVariables>(G.ActivateBaselineVersionDocument, { input: { originVersionId: reviewId, purpose, expectedOriginVersion: draft.version.originRevision, counterpartyParticipantIds: activationParticipantIds, clientMutationId: newClientMutationId() } });
      throwUserErrors(result.activateBaselineVersion.userErrors);
      const version = result.activateBaselineVersion.version;
      activated = `Versão ${version?.versionNumber ?? ""} ativada. Itens incluídos: ${version?.counts?.included ?? 0}; rejeitados: ${version?.counts?.rejected ?? 0}; não revisados: ${version?.counts?.notReviewed ?? 0}.`;
      await refreshDraft(false);
    }, false);
    if (activated) setStatus(activated);
    return Boolean(activated);
  };
  const discard = () => run(async () => {
    const result = await graphql<G.DiscardBaselineDraftMutation, G.DiscardBaselineDraftMutationVariables>(G.DiscardBaselineDraftDocument, { input: { originVersionId: reviewId, clientMutationId: newClientMutationId() } });
    throwUserErrors(result.discardBaselineDraft.userErrors);
    router.push("/baselines");
  }, false);
  const retry = () => run(async () => {
    const result = await graphql<G.RetryInventoryDraftMutation, G.RetryInventoryDraftMutationVariables>(G.RetryInventoryDraftDocument, { input: { originVersionId: reviewId, clientMutationId: newClientMutationId() } });
    throwUserErrors(result.retryInventoryDraft.userErrors);
  });
  const regenerateVoiceSuggestions = (environment: ReviewEnvironment) => run(async () => {
    const voiceSources = environment.items
      .filter((item) => item.decision === "PENDING")
      .flatMap((item) => item.attributeSources ?? [])
      .filter((source) => /VOICE|TRANSCRIPT/i.test(source.sourceKind) && source.noteId)
      .reduce((sources, source) => sources.set(source.noteId!, source), new Map<string, (typeof environment.items)[number]["attributeSources"][number]>());
    const lateNotes = inspectionNotes.filter((note) => note.requirementKey === environment.requirementKey && selectedLateNoteIds.includes(note.id));
    const revisions = await Promise.all([...new Set([...voiceSources.keys(), ...lateNotes.map((note) => note.id)])].map(async (noteId) => {
      const result = await graphql<G.DashboardAudioNoteQuery, G.DashboardAudioNoteQueryVariables>(G.DashboardAudioNoteDocument, { id: noteId });
      return result.audioNote?.currentRevisionId;
    }));
    const revisionIds = [...new Set(revisions.filter((id): id is string => Boolean(id)))];
    if (revisionIds.length === 0) throw Object.assign(new Error("Selecione uma nota sem item ou aguarde sugestões de voz pendentes neste ambiente."), { isUserError: true });
    const result = await graphql<G.RegenerateInventorySuggestionsMutation, G.RegenerateInventorySuggestionsMutationVariables>(G.RegenerateInventorySuggestionsDocument, {
      input: { originVersionId: reviewId, environmentId: environment.id, expectedVersion: draft?.version.originRevision ?? 0, revisionIds, clientMutationId: newClientMutationId() },
    });
    throwUserErrors(result.regenerateInventorySuggestions.userErrors);
    setSelectedLateNoteIds([]);
    setStatus("Atualização das sugestões de voz solicitada para este ambiente.");
  });
  const activationSummary = draft ? buildActivationSummary(draft.environments.flatMap((environment) => environment.items), draft.progress.pending) : { included: 0, rejected: 0, notReviewed: 0 };
  const voiceSummary = draft?.voiceSummary ?? emptyVoiceSummary;
  const eligibleCounterparties = participants.filter((participant) => participant.businessUnitId === businessUnitId);
  const reviewGroups = draft ? buildReviewGroups(draft) : [];
  const selectedEnvironment = reviewGroups.find((environment) => environment.id === selectedReviewEnvironmentId)
    ?? reviewGroups.find((environment) => environment.items.some((item) => item.id === selectedReviewItemId))
    ?? reviewGroups.find((environment) => environment.items.some((item) => item.decision === "PENDING"))
    ?? reviewGroups[0];
  const selectedReviewItem = selectedEnvironment?.items.find((item) => item.id === selectedReviewItemId)
    ?? selectedEnvironment?.items.find((item) => item.decision === "PENDING")
    ?? selectedEnvironment?.items[0];
  const selectedItemIndex = selectedEnvironment?.items.findIndex((item) => item.id === selectedReviewItem?.id) ?? -1;
  const citedNoteIds = new Set(draft?.environments.flatMap((environment) => environment.items.flatMap((item) => (item.attributeSources ?? []).flatMap((source) => source.noteId ?? []))) ?? []);
  const lateNotesByEnvironment = (environment: ReviewEnvironment) => inspectionNotes.filter((note) => note.requirementKey === environment.requirementKey && !citedNoteIds.has(note.id));

  return <section className="baseline-review" aria-labelledby="baseline-review-heading">
    <div className="baseline-review-heading">
      <div><h2 id="baseline-review-heading">Revisar inventário</h2><p role="status">{status}</p></div>
      <Button variant="secondary" onClick={() => { void refreshDraft().then(() => { setReloadConflict(false); setError(""); }).catch((failure) => setError(failureText(failure))); }}>Atualizar</Button>
    </div>
    {error && <p role="alert">{error}</p>}
    {reloadConflict && <p className="baseline-warning">Use “Atualizar” para carregar a versão mais recente do rascunho.</p>}
    {!draft ? <p role="status">Carregando inventário…</p>
      : isPreparingDraft(draft) ? <p role="status">O inventário está sendo preparado. Você pode voltar a esta página mais tarde.</p>
        : draft.readOnly ? <><p role="status">Este inventário está em modo somente leitura.</p><ReadOnlyInventory environments={draft.environments} /></>
          : <>
            {draft.version.draftState === "FAILED" && <div role="alert"><p>Não foi possível preparar este inventário.</p><Button variant="secondary" disabled={busy} onClick={() => void retry()}>Tentar novamente</Button></div>}
            {draft.warnings.map((warning) => <p className="baseline-warning" key={warning}>{warning}</p>)}
            {(voiceSummary.pendingVoiceItems + voiceSummary.conflictItems + voiceSummary.staleSuggestions + voiceSummary.notesWithoutItem > 0 || inspectionNotes.length > 0) && <section className="baseline-voice-summary" aria-label="Resumo das anotações de voz">
              <h3>Resumo de voz</h3>
              <dl><div><dt>Itens de voz pendentes</dt><dd>{voiceSummary.pendingVoiceItems}</dd></div><div><dt>Conflitos voz × imagem</dt><dd>{voiceSummary.conflictItems}</dd></div><div><dt>Sugestões baseadas em texto antigo</dt><dd>{voiceSummary.staleSuggestions}</dd></div><div><dt>Notas sem item</dt><dd>{voiceSummary.notesWithoutItem}</dd></div></dl>
              {draft.environments.map((environment) => { const summary = environment.voiceSummary ?? emptyVoiceSummary; return <p key={environment.id}>{environment.name}: {summary.pendingVoiceItems} itens de voz pendentes · {summary.conflictItems} conflitos · {summary.staleSuggestions} sugestões antigas · {summary.notesWithoutItem} notas sem item</p>; })}
            </section>}
            <div className="baseline-review-workspace">
              <aside className="baseline-review-rooms" aria-label="Navegação dos ambientes">
                <div className="baseline-review-rooms-heading"><h3>Ambientes</h3><span>{reviewGroups.length}</span></div>
                {reviewGroups.map((environment) => {
                  const isSelectedEnvironment = environment.id === selectedEnvironment?.id;
                  return <section className={`baseline-review-room${isSelectedEnvironment ? " is-selected" : ""}`} key={environment.id}>
                    <button type="button" className="baseline-room-select" aria-current={isSelectedEnvironment ? "true" : undefined} onClick={() => {
                      const nextItem = environment.items.find((item) => item.decision === "PENDING") ?? environment.items[0];
                      setSelectedReviewEnvironmentId(environment.id);
                      setSelectedReviewItemId(nextItem?.id);
                    }}>
                      <strong>{environment.name}</strong>
                      <span>{environment.items.length} {environment.items.length === 1 ? "item" : "itens"} · {environment.items.filter((item) => item.decision === "PENDING").length} pendentes</span>
                    </button>
                    <ul className="baseline-room-items" aria-label={`Itens de ${environment.name}`}>
                      {environment.items.map((item) => <li key={item.id}>
                        <button type="button" className="baseline-room-item" aria-current={item.id === selectedReviewItem?.id ? "true" : undefined} onClick={() => { setSelectedReviewEnvironmentId(environment.id); setSelectedReviewItemId(item.id); }}>
                          <span>{item.name}</span><small>{decisionLabel(item.decision)}</small>
                        </button>
                      </li>)}
                    </ul>
                    <details className="baseline-room-actions">
                      <summary>Ações do ambiente</summary>
                      <div>
                        {lateNotesByEnvironment(environment).length > 0 && <fieldset className="baseline-late-voice-notes"><legend>Notas sem item em {environment.name}</legend><p>Selecione notas para incluí-las em uma regeneração explícita. Itens já decididos não serão alterados.</p>{lateNotesByEnvironment(environment).map((note) => <label key={note.id}><input type="checkbox" checked={selectedLateNoteIds.includes(note.id)} onChange={(event) => setSelectedLateNoteIds((current) => event.target.checked ? [...current, note.id] : current.filter((id) => id !== note.id))} />{note.noSpeech ? "Nenhuma fala identificada" : note.revisions.find((revision) => revision.id === note.currentRevisionId)?.text || "Texto ainda indisponível"}</label>)}<Button variant="secondary" disabled={busy || !lateNotesByEnvironment(environment).some((note) => selectedLateNoteIds.includes(note.id))} onClick={() => void regenerateVoiceSuggestions(environment)}>Regenerar sugestões selecionadas</Button></fieldset>}
                        {environment.items.some((item) => item.decision === "PENDING" && (item.attributeSources ?? []).some((source) => /VOICE|TRANSCRIPT/i.test(source.sourceKind) && source.noteId)) && <Button variant="secondary" disabled={busy} onClick={() => void regenerateVoiceSuggestions(environment)}>Atualizar sugestões de voz pendentes</Button>}
                        <Button variant="secondary" disabled={busy} onClick={() => openDialog(() => setBulkEnvironment(environment))}>Aceitar itens confiáveis</Button>
                        <Button variant="secondary" disabled={busy} onClick={() => openDialog(() => setEditingEnvironment(environment))}>Editar ambiente</Button>
                        <Button variant="secondary" disabled={busy} onClick={() => openDialog(() => setRemovingEnvironment(environment))}>Remover ambiente</Button>
                      </div>
                    </details>
                  </section>;
                })}
                <Button variant="secondary" disabled={busy} onClick={() => openDialog(() => setEditingEnvironment(null))}>Adicionar ambiente</Button>
              </aside>
              <section className="baseline-review-item-panel" aria-label="Item selecionado" aria-live="polite">
                {selectedReviewItem && selectedEnvironment ? <>
                  <header className="baseline-review-item-heading">
                    <div><p>{selectedEnvironment.name} · Item {selectedItemIndex + 1} de {selectedEnvironment.items.length}</p><h3>{selectedReviewItem.name}</h3></div>
                    <span className={`baseline-item-status baseline-item-status--${selectedReviewItem.decision.toLowerCase()}`}>{decisionLabel(selectedReviewItem.decision)}</span>
                  </header>
                  <div className="baseline-review-item-content">
                    <EvidenceStrip item={selectedReviewItem} frameLabel="Quadro de vídeo de" />
                    <div className="baseline-item-detail">
                      <p>{itemTypeLabel(selectedReviewItem.typeCode)} · {conditionLabel(selectedReviewItem.condition)}</p>
                      <p>{itemAttributes(selectedReviewItem)}</p>
                      {(selectedReviewItem.attributeSources ?? []).map((source) => /VOICE|TRANSCRIPT/i.test(source.sourceKind) ? <details key={source.id} className="baseline-voice-source">
                        <summary>Informado por voz · {source.attributeName}</summary>
                        {source.stale && <p role="status">Baseado em revisão anterior; a revisão citada permanece visível abaixo.</p>}
                        {source.noteId && source.revisionId ? <AudioNoteReview noteId={source.noteId} revisionId={source.revisionId} attributeName={source.attributeName} /> : <p>A referência exata da anotação não está disponível.</p>}
                      </details> : <p className="baseline-attribute-source" key={source.id}>{/VISUAL|IMAGE/i.test(source.sourceKind) ? "Observado visualmente" : source.reviewedBy ? "Revisado por operador" : "Fonte registrada"} · {source.attributeName}</p>)}
                      {(selectedReviewItem.voiceConflicts ?? []).map((conflict, index) => <div className="baseline-voice-conflict" key={`${conflict.attributeName}-${index}`} role="status"><strong>Revisão necessária · {conflict.attributeName}</strong><div><span>Imagem: {conflict.visualValue || "sem valor"}</span><span>Voz: {conflict.voiceValue || "sem valor"}</span></div></div>)}
                      <p className="baseline-confidence">{selectedReviewItem.confidenceBps == null ? "Sem confiança informada" : `Confiança ${Math.round(selectedReviewItem.confidenceBps / 100)}%`} · {selectedReviewItem.declared ? "Declarado" : decisionLabel(selectedReviewItem.decision)}</p>
                      <details><summary>Definições das condições</summary><ul>{conditionOptions.map(([, definition]) => <li key={definition}>{definition}</li>)}</ul></details>
                    </div>
                  </div>
                  <div className="baseline-item-actions">
                    <Button aria-label={`Aceitar ${selectedReviewItem.name}`} disabled={busy || selectedReviewItem.decision === "ACCEPTED"} onClick={() => void decide(selectedReviewItem, "ACCEPT")}>Aceitar item</Button>
                    <Button aria-label={`Rejeitar ${selectedReviewItem.name}`} variant="secondary" disabled={busy} onClick={() => openDialog(() => setRejectingItem(selectedReviewItem))}>Rejeitar</Button>
                    <Button aria-label={`Editar ${selectedReviewItem.name}`} variant="secondary" disabled={busy} onClick={() => openDialog(() => setEditingItem(selectedReviewItem))}>Editar item</Button>
                    <details className="baseline-item-more"><summary>Mais ações</summary><div>
                      <Button variant="secondary" disabled={busy} onClick={() => openDialog(() => setMergingItem(selectedReviewItem))}>Mesclar</Button>
                      <Button variant="secondary" disabled={busy} onClick={() => openDialog(() => setSplittingItem(selectedReviewItem))}>Dividir</Button>
                    </div></details>
                  </div>
                </> : <div className="baseline-review-empty-item"><h3>Nenhum item para revisar</h3><p>Adicione um item ou crie um ambiente para começar.</p></div>}
                {selectedEnvironment && <details className="baseline-add-disclosure">
                  <summary>+ Adicionar item em {selectedEnvironment.name}</summary>
                  <form className="baseline-add-item" onSubmit={(event) => addDeclaredItem(event, selectedEnvironment.id)}>
                    <div className="baseline-add-item-heading"><strong>Adicionar item</strong><p>Inclua algo que não apareceu na vistoria.</p></div>
                    <div className="baseline-add-item-fields">
                      <Field label="Nome do item"><Input name="name" required /></Field>
                      <Field label="Tipo"><Select name="typeCode" defaultValue="OTHER">{itemTypeOptions.map(([code, label]) => <option value={code} key={code}>{label}</option>)}</Select></Field>
                      <Field label="Condição"><Select name="condition" defaultValue="GOOD">{conditionOptions.map(([value, label]) => <option value={value} key={value}>{label}</option>)}</Select></Field>
                    </div>
                    {selectedEnvironment.items.some((item) => item.evidence.length) && <fieldset className="baseline-evidence-choices"><legend>Evidências existentes (opcional)</legend>
                      {selectedEnvironment.items.flatMap((item) => item.evidence.map((evidence) => <label className="baseline-choice" key={evidence.id}>
                        <input type="checkbox" name="evidenceIds" value={evidence.id} />{item.name} · {evidence.kind === "VIDEO_FRAME" ? "Quadro de vídeo" : "Foto"}{evidence.timestampMs != null ? ` · ${formatEvidenceTime(evidence.timestampMs)}` : ""}
                      </label>))}
                    </fieldset>}
                    <Button type="submit" disabled={busy}>Adicionar item</Button>
                  </form>
                </details>}
              </section>
            </div>
            <section className="baseline-activation">
              <header className="baseline-activation-heading"><div><span>FINALIZAR</span><h3>Ativar versão</h3><p>{activationSummary.included} incluídos · {activationSummary.rejected} rejeitados · {activationSummary.notReviewed} não revisados</p></div>
                <Button disabled={busy || !purpose} onClick={() => openDialog(() => setConfirmActivation(true))}>Ativar inventário</Button>
              </header>
              <div className="baseline-activation-controls">
                <Field label="Propósito da versão"><Select value={purpose} onChange={(event) => setPurpose(event.target.value)}><option value="">Selecione</option>{purposeOptions.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</Select></Field>
                <details className="baseline-invite-disclosure"><summary>Convidar pessoas (opcional){activationParticipantIds.length > 0 ? ` · ${activationParticipantIds.length} selecionada(s)` : ""}</summary>
                  <fieldset className="baseline-evidence-choices"><legend>Pessoas da unidade</legend>
                    {eligibleCounterparties.length ? eligibleCounterparties.map((participant) => <label className="baseline-choice" key={participant.id}>
                      <input type="checkbox" checked={activationParticipantIds.includes(participant.id)} onChange={(event) => setActivationParticipantIds((current) => event.target.checked ? [...current, participant.id] : current.filter((id) => id !== participant.id))} />{participant.name}
                    </label>) : <p>Nenhuma pessoa disponível para convite.</p>}
                    {participantsHaveMore && <Button type="button" variant="secondary" disabled={busy} onClick={() => void loadParticipants(participantsAfter).catch((failure) => setError(failureText(failure)))}>Carregar mais pessoas</Button>}
                  </fieldset>
                </details>
              </div>
              <Button className="baseline-discard-action" variant="secondary" disabled={busy} onClick={() => openDialog(() => setConfirmDiscard(true))}>Descartar rascunho</Button>
            </section>
          </>}
    <FormDialog isOpen={Boolean(rejectingItem)} onClose={() => setRejectingItem(undefined)} title="Rejeitar item" busy={busy} error={error}>
      <RejectItemForm itemName={rejectingItem?.name ?? ""} onSubmit={async (reason, reasonText) => { if (!rejectingItem) return; if (await decide(rejectingItem, "REJECT", null, reason, reasonText)) setRejectingItem(undefined); }} />
    </FormDialog>
    <FormDialog isOpen={Boolean(editingItem)} onClose={() => setEditingItem(undefined)} title="Editar item" busy={busy} error={error}>
      <EditItemForm item={editingItem} onSubmit={async (patch) => { if (!editingItem) return; if (await decide(editingItem, "EDIT", patch)) setEditingItem(undefined); }} />
    </FormDialog>
    <FormDialog isOpen={Boolean(mergingItem)} onClose={() => setMergingItem(undefined)} title="Mesclar itens" busy={busy} error={error}>
      <MergeItemsForm items={draft?.environments.find((environment) => environment.id === mergingItem?.environmentId)?.items ?? []} currentId={mergingItem?.id ?? ""} onSubmit={async (itemIds) => {
        if (!mergingItem) return;
        if (await run(async () => {
          const result = await graphql<G.MergeInventoryItemsMutation, G.MergeInventoryItemsMutationVariables>(G.MergeInventoryItemsDocument, { input: { originVersionId: reviewId, itemIds, resultEnvironmentId: mergingItem.environmentId, clientMutationId: newClientMutationId() } });
          throwUserErrors(result.mergeInventoryItems.userErrors);
        })) setMergingItem(undefined);
      }} />
    </FormDialog>
    <FormDialog isOpen={Boolean(splittingItem)} onClose={() => setSplittingItem(undefined)} title="Dividir item" busy={busy} error={error}>
      <SplitItemForm item={splittingItem} onSubmit={async (parts) => {
        if (!splittingItem) return;
        if (await run(async () => {
          const result = await graphql<G.SplitInventoryItemMutation, G.SplitInventoryItemMutationVariables>(G.SplitInventoryItemDocument, { input: { originVersionId: reviewId, itemId: splittingItem.id, expectedVersion: splittingItem.version, parts, clientMutationId: newClientMutationId() } });
          throwUserErrors(result.splitInventoryItem.userErrors);
        })) setSplittingItem(undefined);
      }} />
    </FormDialog>
    <FormDialog isOpen={editingEnvironment !== undefined} onClose={() => setEditingEnvironment(undefined)} title={editingEnvironment ? "Editar ambiente" : "Adicionar ambiente"} busy={busy} error={error}>
      <EnvironmentForm environment={editingEnvironment ?? undefined} onSubmit={async ({ name, typeCode }) => {
        if (await run(async () => {
          const result = await graphql<G.UpsertInventoryEnvironmentMutation, G.UpsertInventoryEnvironmentMutationVariables>(G.UpsertInventoryEnvironmentDocument, { input: { originVersionId: reviewId, environmentId: editingEnvironment?.id ?? null, typeCode, name, expected: editingEnvironment?.expected ?? true, clientMutationId: newClientMutationId() } });
          throwUserErrors(result.upsertInventoryEnvironment.userErrors);
        })) setEditingEnvironment(undefined);
      }} />
    </FormDialog>
    <ConfirmationDialog isOpen={Boolean(removingEnvironment)} onClose={() => setRemovingEnvironment(undefined)} title="Remover ambiente" target={removingEnvironment?.name ?? "Ambiente"} scope="Inventário em revisão" consequence="O ambiente e os itens associados serão excluídos deste inventário." confirmLabel="Remover ambiente" onConfirm={async () => {
      if (!removingEnvironment) return;
      if (await run(async () => {
        const result = await graphql<G.RemoveInventoryEnvironmentMutation, G.RemoveInventoryEnvironmentMutationVariables>(G.RemoveInventoryEnvironmentDocument, { input: { originVersionId: reviewId, environmentId: removingEnvironment.id, confirmItemExclusion: true, clientMutationId: newClientMutationId() } });
        throwUserErrors(result.removeInventoryEnvironment.userErrors);
      })) setRemovingEnvironment(undefined);
      else mustSucceed(false);
    }} />
    <ConfirmationDialog isOpen={Boolean(bulkEnvironment)} onClose={() => setBulkEnvironment(undefined)} title="Aceitar itens confiáveis" target={bulkEnvironment?.name ?? "Ambiente"} scope="Ambiente selecionado" consequence={`${bulkEnvironment?.items.filter((item) => item.decision === "PENDING" && (item.confidenceBps ?? 0) >= bulkMinimumConfidenceBps).length ?? 0} item(ns) com confiança mínima de 85% serão aceitos.`} confirmLabel="Aceitar itens" onConfirm={async () => {
      if (!bulkEnvironment) return;
      let accepted = "";
      await run(async () => {
        const result = await graphql<G.BulkAcceptInventoryEnvironmentMutation, G.BulkAcceptInventoryEnvironmentMutationVariables>(G.BulkAcceptInventoryEnvironmentDocument, { input: { originVersionId: reviewId, environmentId: bulkEnvironment.id, minimumConfidenceBps: bulkMinimumConfidenceBps, clientMutationId: newClientMutationId() } });
        throwUserErrors(result.bulkAcceptInventoryEnvironment.userErrors);
        accepted = `${result.bulkAcceptInventoryEnvironment.acceptedCount} itens aceitos em lote com confiança mínima de 85%.`;
        await refreshDraft(false);
      }, false);
      if (!accepted) mustSucceed(false);
      setStatus(accepted);
      setBulkEnvironment(undefined);
    }} />
    <ConfirmationDialog isOpen={confirmActivation} onClose={() => setConfirmActivation(false)} title="Ativar inventário" target={`Versão ${draft?.version.versionNumber ?? ""}`} scope="Imóvel selecionado" consequence={`${draft?.progress.pending ?? 0} item(ns) não revisado(s) ficarão registrados como tal.`} confirmLabel="Ativar inventário" onConfirm={async () => { mustSucceed(await activate()); setConfirmActivation(false); }} />
    <ConfirmationDialog isOpen={confirmDiscard} onClose={() => setConfirmDiscard(false)} title="Descartar rascunho" target={`Versão ${draft?.version.versionNumber ?? ""}`} scope="Inventário em revisão" consequence="O descarte ficará registrado no histórico e a versão não poderá ser ativada." confirmLabel="Descartar rascunho" onConfirm={async () => { mustSucceed(await discard()); setConfirmDiscard(false); }} />
  </section>;
}
