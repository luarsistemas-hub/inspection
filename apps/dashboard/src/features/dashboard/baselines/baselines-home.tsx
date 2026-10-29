"use client";

import { useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { Button, Field, Select } from "@inspection/design-system";
import { graphql } from "@/graphql/client";
import * as G from "@/graphql/generated";
import { baselineVersionPresentation, versionSourceLabel } from "../inventory-presentation";
import { PublishedInventory } from "./published-inventory";
import { failureText, inventoryAvailabilityLabel, newClientMutationId, throwUserErrors, type BaselineVersion } from "./review-model";
import { CounterpartyList } from "./version-actions";
import { InspectionOriginPromotion } from "../inspection-origin-promotion";

type AssetOption = { id: string; name: string; externalKey: string };
type UserError = { code: string; field?: string | null; message: string };
const completedInspectionPages = 5;

/** existingDraftId returns the open draft reported by a draft-creation conflict. */
function existingDraftId(errors: UserError[]) {
  const conflict = errors.find((issue) => issue.code === "CONFLICT" && issue.field === "originVersionId");
  return conflict && /^[0-9a-f-]{36}$/i.test(conflict.message) ? conflict.message : undefined;
}
type CompletedInspection = G.InspectionsQuery["inspections"]["nodes"][number];

/** BaselinesHome lists an asset's baseline versions and, for customers, its active inventory. */
export function BaselinesHome({ canReview, isCustomer, readHistory, canReadTrail = false }: { canReview: boolean; isCustomer: boolean; readHistory: boolean; canReadTrail?: boolean }) {
  const router = useRouter();
  const [assets, setAssets] = useState<AssetOption[]>([]);
  const [assetId, setAssetId] = useState("");
  const [assetAfter, setAssetAfter] = useState<string | null>(null);
  const [assetsHaveMore, setAssetsHaveMore] = useState(false);
  const [showAssetPicker, setShowAssetPicker] = useState(false);
  const [versions, setVersions] = useState<BaselineVersion[]>([]);
  const [status, setStatus] = useState("Carregando inventários…");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [completedInspections, setCompletedInspections] = useState<CompletedInspection[]>([]);
  const [visibleInspectionCount, setVisibleInspectionCount] = useState(3);
  const [expandedInspectionId, setExpandedInspectionId] = useState<string | null>(null);
  const [inventory, setInventory] = useState<G.DashboardBaselineInventoryQuery["baselineInventory"]>();
  const [selectedReadOnlyVersion, setSelectedReadOnlyVersion] = useState<string | null>(null);
  const showHistory = readHistory && !isCustomer;
  const [participantNames, setParticipantNames] = useState(new Map<string, string>());
  const [memberNames, setMemberNames] = useState(new Map<string, string>());
  const draftVersion = canReview ? versions.find((version) => version.status === "DRAFT") : undefined;
  const activeVersion = canReview && !draftVersion ? versions.find((version) => version.status === "ACTIVE") : undefined;
  const focusedVersion = draftVersion ?? activeVersion;
  const historicalVersions = showHistory ? versions.filter((version) => version.id !== focusedVersion?.id) : [];
  const orderedInspections = [...completedInspections].sort((first, second) => new Date(second.dueAt ?? 0).getTime() - new Date(first.dueAt ?? 0).getTime());
  const selectedAsset = assets.find((asset) => asset.id === assetId);
  const hasActiveVersion = versions.some((version) => version.status === "ACTIVE");

  const loadAssets = useCallback(async (after: string | null) => {
    let nodes: AssetOption[];
    let pageInfo: { endCursor?: string | null; hasNextPage: boolean };
    if (isCustomer) {
      const result = await graphql<G.DashboardCustomerBaselineAssetsQuery, G.DashboardCustomerBaselineAssetsQueryVariables>(G.DashboardCustomerBaselineAssetsDocument, { after });
      nodes = result.customerPortfolio.nodes.map((item) => ({ id: item.assetId, name: item.assetName, externalKey: item.assetExternalKey }));
      pageInfo = result.customerPortfolio.pageInfo;
    } else {
      const result = await graphql<G.DashboardBaselineAssetsQuery, G.DashboardBaselineAssetsQueryVariables>(G.DashboardBaselineAssetsDocument, { after });
      nodes = result.assets.nodes;
      pageInfo = result.assets.pageInfo;
    }
    setAssets((current) => after ? [...current, ...nodes] : nodes);
    setAssetAfter(pageInfo.endCursor ?? null);
    setAssetsHaveMore(pageInfo.hasNextPage);
    if (!after) setAssetId((value) => value || nodes[0]?.id || "");
  }, [isCustomer]);
  const refreshVersions = useCallback(async () => {
    if (!assetId || !showHistory) {
      setVersions([]);
      if (!assetId) setStatus("Selecione um imóvel.");
      return;
    }
    setStatus("Carregando versões…");
    const result = await graphql<G.DashboardBaselineVersionsQuery, G.DashboardBaselineVersionsQueryVariables>(G.DashboardBaselineVersionsDocument, { assetId });
    setVersions(result.baselineVersions.nodes);
    const count = result.baselineVersions.nodes.length;
    setStatus(count === 0 ? "Nenhuma versão cadastrada." : `${count} ${count === 1 ? "versão disponível" : "versões disponíveis"}.`);
  }, [assetId, showHistory]);
  useEffect(() => { void loadAssets(null).catch((failure) => setError(failureText(failure))); }, [loadAssets]);
  useEffect(() => { setVisibleInspectionCount(3); setExpandedInspectionId(null); }, [assetId]);
  // Names are best effort: a role without these lists still sees the history.
  useEffect(() => {
    if (!showHistory) return;
    void loadNames(async (after) => {
      const result = await graphql<G.DashboardCounterpartyOptionsQuery, G.DashboardCounterpartyOptionsQueryVariables>(G.DashboardCounterpartyOptionsDocument, { after });
      return { nodes: result.participants.nodes, pageInfo: result.participants.pageInfo };
    }).then(setParticipantNames).catch(() => setParticipantNames(new Map()));
    void loadNames(async (after) => {
      const result = await graphql<G.DashboardReviewersQuery, G.DashboardReviewersQueryVariables>(G.DashboardReviewersDocument, { first: 100, after });
      return { nodes: result.memberships.nodes, pageInfo: result.memberships.pageInfo };
    }).then(setMemberNames).catch(() => setMemberNames(new Map()));
  }, [showHistory]);
  useEffect(() => { void refreshVersions().catch((failure) => setError(failureText(failure))); }, [refreshVersions]);
  // Completed inspections are searched by the asset key, then narrowed to the asset.
  const externalKey = assets.find((asset) => asset.id === assetId)?.externalKey;
  useEffect(() => {
    setCompletedInspections([]);
    if (!canReview || !assetId) return;
    let cancelled = false;
    const load = async () => {
      const found: CompletedInspection[] = [];
      let after: string | null = null;
      for (let page = 0; page < completedInspectionPages; page += 1) {
        const result: G.InspectionsQuery = await graphql<G.InspectionsQuery, G.InspectionsQueryVariables>(G.InspectionsDocument, { first: 100, after, history: true, search: externalKey || null, statusGroup: "COMPLETED" });
        found.push(...result.inspections.nodes.filter((inspection) => inspection.assetId === assetId && inspection.status === "COMPLETED"));
        if (!result.inspections.pageInfo.hasNextPage || !result.inspections.pageInfo.endCursor) break;
        after = result.inspections.pageInfo.endCursor;
      }
      if (!cancelled) setCompletedInspections(found);
    };
    void load().catch((failure) => { if (!cancelled) { setCompletedInspections([]); setError(failureText(failure)); } });
    return () => { cancelled = true; };
  }, [assetId, canReview, externalKey]);
  useEffect(() => {
    if (!assetId || canReview || (!isCustomer && !readHistory)) return;
    void graphql<G.DashboardBaselineInventoryQuery, G.DashboardBaselineInventoryQueryVariables>(G.DashboardBaselineInventoryDocument, { assetId, versionId: selectedReadOnlyVersion })
      .then((result) => {
        setInventory(result.baselineInventory);
        if (!showHistory) setStatus(result.baselineInventory?.hasInventory ? "Inventário carregado." : "Este imóvel ainda não tem inventário ativo.");
      })
      .catch((failure) => setError(failureText(failure)));
  }, [assetId, canReview, isCustomer, readHistory, selectedReadOnlyVersion, showHistory]);
  useEffect(() => { if (!showHistory && !assetId && assets.length === 0) setStatus("Nenhum imóvel disponível."); }, [assetId, assets.length, showHistory]);

  const startDraft = async (create: () => Promise<{ versionId?: string | null; errors: UserError[] }>) => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      const { versionId, errors } = await create();
      const existing = existingDraftId(errors);
      if (existing) {
        setStatus("Já existe um rascunho aberto para este imóvel; abrindo o rascunho existente.");
        router.push(`/baselines/review/${existing}`);
        return;
      }
      throwUserErrors(errors);
      if (versionId) router.push(`/baselines/review/${versionId}`);
      else await refreshVersions();
    } catch (failure) {
      setError(failureText(failure));
    } finally {
      setBusy(false);
    }
  };
  const requestCataloging = () => startDraft(async () => {
    const result = await graphql<G.RequestOriginCatalogingMutation, G.RequestOriginCatalogingMutationVariables>(G.RequestOriginCatalogingDocument, { input: { assetId, clientMutationId: newClientMutationId() } });
    return { versionId: result.requestOriginCataloging.version?.id, errors: result.requestOriginCataloging.userErrors };
  });

  const startDraftFromInspection = (inspectionId: string) => startDraft(async () => {
    const result = await graphql<G.StartBaselineDraftFromInspectionMutation, G.StartBaselineDraftFromInspectionMutationVariables>(G.StartBaselineDraftFromInspectionDocument, { input: { inspectionId, clientMutationId: newClientMutationId() } });
    return { versionId: result.startBaselineDraftFromInspection.version?.id, errors: result.startBaselineDraftFromInspection.userErrors };
  });

  return <section aria-label="Inventários do imóvel" className="baselines-page">
    <p className="baselines-intro">Acompanhe o inventário do imóvel e continue de onde parou.</p>
    {error && <p role="alert" className="baselines-error">{error}</p>}
    <div className="baselines-property">
      <div className="baselines-property-summary"><span>IMÓVEL</span><strong>{selectedAsset?.name || "Selecione um imóvel"}</strong></div>
      <Button type="button" variant="secondary" aria-expanded={showAssetPicker} aria-controls={showAssetPicker ? "baselines-asset-picker" : undefined} onClick={() => setShowAssetPicker((value) => !value)}>Trocar imóvel <span aria-hidden="true">⌄</span></Button>
      {showAssetPicker && <div id="baselines-asset-picker" className="baselines-asset-picker">
        <Field label="Selecione um imóvel"><Select value={assetId} onChange={(event) => { setVersions([]); setAssetId(event.target.value); setShowAssetPicker(false); }}><option value="">Selecione um imóvel</option>{assets.map((asset) => <option key={asset.id} value={asset.id}>{asset.name}</option>)}</Select></Field>
        {assetsHaveMore && <Button type="button" variant="secondary" onClick={() => void loadAssets(assetAfter).catch((failure) => setError(failureText(failure)))}>Carregar mais imóveis</Button>}
      </div>}
    </div>
    <p role="status" className="baselines-visually-hidden">{status}</p>
    {canReview && <div className="baselines-workspace">
      <section className="baselines-current" aria-labelledby="baselines-current-heading">
        {draftVersion ? <>
          <div className="baseline-state-line"><span aria-hidden="true" /><strong>{hasActiveVersion ? "Há um inventário ativo" : "Nenhum inventário ativo"}</strong> · Há um rascunho em andamento.</div>
          <article className="baseline-focus-card">
            <div className="baseline-focus-heading"><span className="baseline-eyebrow">CONTINUE DE ONDE PAROU</span><span className="baseline-status baseline-status--draft">Rascunho</span></div>
            <h3 id="baselines-current-heading">{`Versão ${draftVersion.versionNumber}`}</h3>
            <p>Esta versão ainda não foi ativada. Revise os itens e escolha o propósito antes de publicar.</p>
            <div className="baseline-focus-counts"><span>{draftVersion.counts?.included ?? 0} itens incluídos</span><span>{draftVersion.counts?.rejected ?? 0} rejeitados</span><span>{draftVersion.counts?.notReviewed ?? 0} não revisados</span></div>
            <div className="baseline-focus-actions">
              <Button disabled={busy || draftVersion.draftState === "PREPARING"} onClick={() => router.push(`/baselines/review/${encodeURIComponent(draftVersion.id)}`)}>{draftVersion.draftState === "PREPARING" ? "Preparando inventário…" : "Revisar rascunho →"}</Button>
              {canReadTrail && draftVersion.hasInventory && <button type="button" className="baselines-text-action" onClick={() => router.push(`/baselines/trail/${encodeURIComponent(draftVersion.id)}`)}>Trilha de auditoria</button>}
            </div>
          </article>
        </> : activeVersion ? <>
          <div className="baseline-state-line baseline-state-line--active"><span aria-hidden="true" />Este imóvel tem um inventário ativo</div>
          <article className="baseline-focus-card">
            <div className="baseline-focus-heading"><span className="baseline-eyebrow">VERSÃO ATUAL</span><span className="baseline-status baseline-status--active">Ativa</span></div>
            <h3 id="baselines-current-heading">{`Versão ${activeVersion.versionNumber}`}</h3>
            <p>{baselineVersionPresentation(activeVersion, activeVersion.activatedBy ? memberNames.get(activeVersion.activatedBy) : undefined).purpose} · {versionSourceLabel(activeVersion.source)}</p>
            <div className="baseline-focus-actions">
              {activeVersion.hasInventory && <Button onClick={() => router.push(`/baselines/review/${encodeURIComponent(activeVersion.id)}`)}>Ver inventário →</Button>}
              {canReadTrail && activeVersion.hasInventory && <button type="button" className="baselines-text-action" onClick={() => router.push(`/baselines/trail/${encodeURIComponent(activeVersion.id)}`)}>Trilha de auditoria</button>}
            </div>
          </article>
        </> : <>
          <div className="baseline-state-line"><span aria-hidden="true" />Nenhum inventário ativo</div>
          <article className="baseline-focus-card baseline-empty-focus">
            <div className="baseline-focus-heading"><span className="baseline-eyebrow">PRÓXIMO PASSO</span></div>
            <h3 id="baselines-current-heading">Escolha uma vistoria para começar</h3>
            <p>Você também pode catalogar um inventário existente para este imóvel.</p>
          </article>
        </>}
      </section>
      <section className="baselines-inspections" aria-labelledby="baselines-inspections-heading">
        <header className="baselines-section-heading">
          <div><h3 id="baselines-inspections-heading">Vistorias concluídas</h3><p>Use uma vistoria para criar uma nova origem ou um rascunho.</p></div>
          <button type="button" className="baselines-text-action" disabled={!assetId || busy} onClick={() => void requestCataloging()}>Catalogar origem existente</button>
        </header>
        {completedInspections.length ? <ul className="baseline-inspection-list">
          {orderedInspections.slice(0, visibleInspectionCount).map((inspection) => <li key={inspection.id}>
            <div className="baseline-inspection-row"><div className="baseline-inspection-summary"><strong>{inspection.dueAt ? `Vistoria de ${new Date(inspection.dueAt).toLocaleDateString("pt-BR")}` : "Vistoria concluída"}</strong><span>{inspection.participantName || "Responsável indisponível"} · Vistoria concluída</span></div>
              <Button type="button" variant="secondary" aria-expanded={expandedInspectionId === inspection.id} aria-controls={expandedInspectionId === inspection.id ? `baseline-inspection-actions-${inspection.id}` : undefined} onClick={() => setExpandedInspectionId((current) => current === inspection.id ? null : inspection.id)}>Escolher ação <span aria-hidden="true">⌄</span></Button></div>
            {expandedInspectionId === inspection.id && <div id={`baseline-inspection-actions-${inspection.id}`} className="baseline-inspection-actions">
              <InspectionOriginPromotion inspectionId={inspection.id} assetId={inspection.assetId} inspectionLabel={`Vistoria de ${inspection.participantName || "responsável indisponível"}`} onCompleted={() => void refreshVersions().catch((failure) => setError(failureText(failure)))} />
              <Button variant="secondary" disabled={busy} onClick={() => void startDraftFromInspection(inspection.id)}>Iniciar rascunho</Button>
            </div>}
          </li>)}
        </ul> : <p className="baseline-empty-inspections">Nenhuma vistoria concluída encontrada para este imóvel.</p>}
        {orderedInspections.length > visibleInspectionCount && <Button className="baseline-show-more" type="button" variant="secondary" onClick={() => setVisibleInspectionCount((count) => count + 3)}>Ver mais vistorias ({orderedInspections.length - visibleInspectionCount})</Button>}
      </section>
    </div>}
    {!canReview && inventory?.version && <PublishedInventory inventory={inventory} isCustomer={isCustomer} />}
    {!canReview && inventory && !inventory.hasInventory && <p>Este imóvel ainda não tem inventário ativo.</p>}
    {showHistory && <section className="baselines-history" aria-labelledby="baselines-history-heading">
      <header className="baselines-section-heading"><div><h3 id="baselines-history-heading">{canReview ? "Histórico de versões" : "Versões do inventário"}</h3><p>{versionCountLabel((canReview ? historicalVersions : versions).length, canReview)}</p></div></header>
      {(canReview ? historicalVersions : versions).length === 0 ? <p>Nenhuma versão anterior para este imóvel.</p> : <ol className="baseline-version-list">
        {(canReview ? historicalVersions : versions).map((version) => <li key={version.id}><details className="baseline-history-item"><summary><span><strong>{`Versão ${version.versionNumber}`}</strong> · {baselineVersionPresentation(version).status} · {versionSourceLabel(version.source)}</span><span className="baseline-history-open">Ver detalhes</span></summary><VersionCard version={version} assetId={assetId} canReview={canReview} canReadTrail={canReadTrail} participantNames={participantNames} authorName={version.activatedBy ? memberNames.get(version.activatedBy) : undefined} onView={() => canReview ? router.push(`/baselines/review/${version.id}`) : setSelectedReadOnlyVersion(version.id)} onChanged={() => void refreshVersions()} /></details></li>)}
      </ol>}
    </section>}
  </section>;
}

/** VersionCard summarizes one baseline version and its pending actions. */
function VersionCard({ version, assetId, canReview, canReadTrail, participantNames, authorName, onView, onChanged }: { version: BaselineVersion; assetId: string; canReview: boolean; canReadTrail: boolean; participantNames: Map<string, string>; authorName?: string; onView: () => void; onChanged: () => void }) {
  const router = useRouter();
  const presentation = baselineVersionPresentation(version, authorName);
  const pinnable = canReview && version.hasInventory && ["ACTIVE", "SUPERSEDED"].includes(version.status);
  return <article>
    <header><h3>{presentation.title} · {presentation.purpose}</h3><span>{presentation.status}</span></header>
    <p>{presentation.activatedAt} · {presentation.author} · {versionSourceLabel(version.source)}</p>
    <p>{inventoryAvailabilityLabel(version.sourceAvailability)}</p>
    <p>Incluídos {version.counts?.included ?? 0} · rejeitados {version.counts?.rejected ?? 0} · não revisados {version.counts?.notReviewed ?? 0}</p>
    <p>Contestações: {presentation.contestation}</p>
    {version.draftState === "PREPARING" && <p role="status">Inventário em preparação</p>}
    {version.hasInventory && <Button variant="secondary" onClick={onView}>{canReview && version.status === "DRAFT" ? "Revisar rascunho" : "Ver inventário"}</Button>}
    {canReadTrail && version.hasInventory && <Button variant="secondary" onClick={() => router.push(`/baselines/trail/${encodeURIComponent(version.id)}`)}>Trilha de auditoria</Button>}
    {pinnable && <Button variant="secondary" onClick={() => router.push(`/inspections?create=1&assetId=${encodeURIComponent(assetId)}&originVersionId=${encodeURIComponent(version.id)}`)}>Criar vistoria com esta versão</Button>}
    <CounterpartyList invitations={version.counterpartyInvitations} participantNames={participantNames} canReview={canReview} onChanged={onChanged} />
  </article>;
}

/** loadNames reads every page of a named list into an id → name map, up to a bounded number of pages. */
async function loadNames(page: (after: string | null) => Promise<{ nodes: Array<{ id: string; name: string }>; pageInfo: { endCursor?: string | null; hasNextPage: boolean } }>) {
  const names = new Map<string, string>();
  let after: string | null = null;
  for (let count = 0; count < 10; count += 1) {
    const result = await page(after);
    for (const node of result.nodes) names.set(node.id, node.name);
    if (!result.pageInfo.hasNextPage || !result.pageInfo.endCursor) break;
    after = result.pageInfo.endCursor;
  }
  return names;
}

function versionCountLabel(count: number, historical: boolean) {
  if (historical) return `${count} ${count === 1 ? "versão anterior" : "versões anteriores"}`;
  return `${count} ${count === 1 ? "versão disponível" : "versões disponíveis"}`;
}
