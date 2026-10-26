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
export function BaselinesHome({ canReview, isCustomer, readHistory }: { canReview: boolean; isCustomer: boolean; readHistory: boolean }) {
  const router = useRouter();
  const [assets, setAssets] = useState<AssetOption[]>([]);
  const [assetId, setAssetId] = useState("");
  const [assetAfter, setAssetAfter] = useState<string | null>(null);
  const [assetsHaveMore, setAssetsHaveMore] = useState(false);
  const [versions, setVersions] = useState<BaselineVersion[]>([]);
  const [status, setStatus] = useState("Carregando inventários…");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [inspectionToStart, setInspectionToStart] = useState("");
  const [completedInspections, setCompletedInspections] = useState<CompletedInspection[]>([]);
  const [inventory, setInventory] = useState<G.DashboardBaselineInventoryQuery["baselineInventory"]>();
  const [selectedReadOnlyVersion, setSelectedReadOnlyVersion] = useState<string | null>(null);
  const showHistory = readHistory && !isCustomer;
  const [participantNames, setParticipantNames] = useState(new Map<string, string>());
  const [memberNames, setMemberNames] = useState(new Map<string, string>());

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
    if (!assetId || !showHistory) { setVersions([]); return; }
    const result = await graphql<G.DashboardBaselineVersionsQuery, G.DashboardBaselineVersionsQueryVariables>(G.DashboardBaselineVersionsDocument, { assetId });
    setVersions(result.baselineVersions.nodes);
    setStatus(`${result.baselineVersions.nodes.length} versão(ões) de inventário carregada(s).`);
  }, [assetId, showHistory]);
  useEffect(() => { void loadAssets(null).catch((failure) => setError(failureText(failure))); }, [loadAssets]);
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
    setInspectionToStart("");
    if (!canReview || !assetId) { setCompletedInspections([]); return; }
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
  const startFromInspection = () => startDraft(async () => {
    const result = await graphql<G.StartBaselineDraftFromInspectionMutation, G.StartBaselineDraftFromInspectionMutationVariables>(G.StartBaselineDraftFromInspectionDocument, { input: { inspectionId: inspectionToStart, clientMutationId: newClientMutationId() } });
    return { versionId: result.startBaselineDraftFromInspection.version?.id, errors: result.startBaselineDraftFromInspection.userErrors };
  });

  return <section aria-labelledby="baselines-heading" className="baselines-page">
    <h2 id="baselines-heading">Inventários dos imóveis</h2>
    <p>Consulte as versões, acompanhe contestações e revise novos inventários.</p>
    {error && <p role="alert">{error}</p>}
    <Field label="Imóvel"><Select value={assetId} onChange={(event) => setAssetId(event.target.value)}><option value="">Selecione um imóvel</option>{assets.map((asset) => <option key={asset.id} value={asset.id}>{asset.name} · {asset.externalKey}</option>)}</Select></Field>
    {assetsHaveMore && <Button type="button" variant="secondary" onClick={() => void loadAssets(assetAfter).catch((failure) => setError(failureText(failure)))}>Carregar mais imóveis</Button>}
    <p role="status">{status}</p>
    {canReview && <div className="baseline-toolbar">
      <Button variant="secondary" disabled={!assetId || busy} onClick={() => void requestCataloging()}>Catalogar origem existente</Button>
      <Field label="Vistoria concluída"><Select value={inspectionToStart} onChange={(event) => setInspectionToStart(event.target.value)} disabled={!assetId}><option value="">Selecione uma vistoria</option>{completedInspections.map((inspection) => <option key={inspection.id} value={inspection.id}>{inspection.participantName} · {inspection.dueAt ? new Date(inspection.dueAt).toLocaleDateString("pt-BR") : "sem data"} · {inspection.id.slice(0, 8)}</option>)}</Select></Field>
      <Button variant="secondary" disabled={!inspectionToStart || busy} onClick={() => void startFromInspection()}>Criar rascunho desta vistoria</Button>
    </div>}
    {!canReview && inventory?.version && <PublishedInventory inventory={inventory} isCustomer={isCustomer} />}
    {!canReview && inventory && !inventory.hasInventory && <p>Este imóvel ainda não tem inventário ativo.</p>}
    {showHistory && (versions.length === 0 ? <p>Nenhuma versão de inventário para este imóvel.</p> : <ol className="baseline-version-list">
      {versions.map((version) => <li key={version.id}><VersionCard version={version} assetId={assetId} canReview={canReview} participantNames={participantNames} authorName={version.activatedBy ? memberNames.get(version.activatedBy) : undefined} onView={() => canReview ? router.push(`/baselines/review/${version.id}`) : setSelectedReadOnlyVersion(version.id)} onChanged={() => void refreshVersions()} /></li>)}
    </ol>)}
  </section>;
}

/** VersionCard summarizes one baseline version and its pending actions. */
function VersionCard({ version, assetId, canReview, participantNames, authorName, onView, onChanged }: { version: BaselineVersion; assetId: string; canReview: boolean; participantNames: Map<string, string>; authorName?: string; onView: () => void; onChanged: () => void }) {
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
