"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { Dialog, IconButton } from "@inspection/design-system";
import { graphQLFailureText, graphql } from "@/graphql/client";
import { InvalidateReportPublicationDocument, PublishReportDocument, ReissueReportDocument, ReviseComparisonPairingDocument, ReportDownloadDocument, ReportWorkspaceDocument, ReportsDocument, type ReportDownloadQuery, type ReportWorkspaceQuery, type ReportsQuery } from "@/graphql/generated";
import { presentClassification, presentReportPDFStatus } from "./presentation";
import { ReportVisual } from "./report-visual";
import { ConfirmationDialog } from "./confirmation-dialog";

type ReportSummary = ReportsQuery["reports"]["nodes"][number];
type Report = NonNullable<ReportWorkspaceQuery["report"]>;
type ReportHistoryRow = ReportSummary;

/** Prevents a superseded report-list request from replacing the active filters. */
export function isCurrentReportListRequest(request: number, currentRequest: number): boolean {
  return request === currentRequest;
}

/** Reports whether the empty-list state should appear after loading. */
export function shouldShowEmptyReportState(itemCount: number, listError?: string | null): boolean {
  return itemCount === 0 && !listError;
}

/** Describes the loaded rows as displayed results, never as the total number of reports. */
export function describeDisplayedReports(count: number, hasMore: boolean): string {
  const displayed = `${count} ${count === 1 ? "laudo exibido" : "laudos exibidos"}`;
  return hasMore ? `${displayed} · há mais laudos para carregar` : displayed;
}

export function ReportsJourney({ canPublish, canCorrectReport = canPublish, refreshKey = 0 }: { canPublish: boolean; canCorrectReport?: boolean; refreshKey?: number }) {
  const router = useRouter();
  const params = useSearchParams();
  const inspectionId = params.get("inspectionId");
  const inspectionIdRef = useRef(inspectionId);
  inspectionIdRef.current = inspectionId;
  const serializedParams = params.toString();
  const [searchDraft, setSearchDraft] = useState(params.get("search") ?? "");
  const [search, setSearch] = useState(params.get("search") ?? "");
  const [classification, setClassification] = useState(params.get("classification") ?? "");
  const [items, setItems] = useState<ReportSummary[]>([]);
  const cursorRef = useRef<string | null>(null);
  const [hasMore, setHasMore] = useState(false);
  const [listLoading, setListLoading] = useState(true);
  const [listError, setListError] = useState("");
  const [report, setReport] = useState<Report | null>(null);
  const [history, setHistory] = useState<ReportHistoryRow[]>([]);
  const [historyMessage, setHistoryMessage] = useState("");
  const [awaitingVersion, setAwaitingVersion] = useState<number | null>(null);
  const [loadedInspectionId, setLoadedInspectionId] = useState("");
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState("");
  const [download, setDownload] = useState<ReportDownloadQuery["reportDownload"]>();
  const [detailMessage, setDetailMessage] = useState("");
  const [invalidating, setInvalidating] = useState(false);
  const listRequest = useRef(0);
  const detailRequest = useRef(0);
  const mediaRefreshes = useRef(0);
  const openedFromList = useRef(false);

  useEffect(() => {
    if (inspectionId) return;
    const current = new URLSearchParams(serializedParams);
    const nextSearch = current.get("search") ?? "";
    const nextClassification = current.get("classification") ?? "";
    setSearchDraft(nextSearch);
    setSearch(nextSearch);
    setClassification(nextClassification);
  }, [inspectionId, serializedParams]);

  useEffect(() => {
    const timer = window.setTimeout(() => setSearch(searchDraft.trim()), 250);
    return () => window.clearTimeout(timer);
  }, [searchDraft]);

  const loadList = useCallback(async (reset: boolean) => {
    const request = ++listRequest.current;
    setListLoading(true);
    setListError("");
    try {
      const after = reset ? null : cursorRef.current;
      const data = await graphql<ReportsQuery, { first: number; after: string | null; search: string | null; classification: string | null; inspectionId: string | null }>(ReportsDocument, { first: 25, after, search: search || null, classification: classification || null, inspectionId: null });
      if (!isCurrentReportListRequest(request, listRequest.current)) return;
      setItems((current) => reset ? data.reports.nodes : [...current, ...data.reports.nodes]);
      cursorRef.current = data.reports.pageInfo.endCursor ?? null;
      setHasMore(data.reports.pageInfo.hasNextPage);
    } catch (error) {
      if (!isCurrentReportListRequest(request, listRequest.current)) return;
      setListError(graphQLFailureText(error));
    } finally {
      if (isCurrentReportListRequest(request, listRequest.current)) setListLoading(false);
    }
  }, [classification, search]);

  useEffect(() => {
    setItems([]);
    cursorRef.current = null;
    void loadList(true);
  }, [search, classification, refreshKey]);

  const requestedVersion = Number(params.get("version")) || null;
  const loadDetail = useCallback(async (id: string, version: number | null = null) => {
    const request = ++detailRequest.current;
    setReport(null);
    setLoadedInspectionId(id);
    setDownload(undefined);
    setDetailMessage("");
    setDetailError("");
    setDetailLoading(true);
    mediaRefreshes.current = 0;
    try {
      const result = await graphql<ReportWorkspaceQuery, { inspectionId: string; version: number | null }>(ReportWorkspaceDocument, { inspectionId: id, version });
      if (request !== detailRequest.current) return;
      if (!result.report) setDetailError("Este laudo ainda não está disponível. Atualize a lista e tente novamente.");
      else setReport(result.report);
    } catch (error) {
      if (request === detailRequest.current) setDetailError(graphQLFailureText(error));
    } finally {
      if (request === detailRequest.current) setDetailLoading(false);
    }
  }, []);

  useEffect(() => {
    if (!inspectionId) {
      detailRequest.current++;
      setReport(null);
      setLoadedInspectionId("");
      setDownload(undefined);
      setDetailLoading(false);
      return;
    }
    void loadDetail(inspectionId, requestedVersion);
  }, [inspectionId, loadDetail, requestedVersion]);

  useEffect(() => {
    if (!report || !inspectionId) { setHistory([]); return; }
    let active = true;
    let timer: number | undefined;
    setHistoryMessage("Carregando histórico de versões…");
    void (async () => {
      try {
        const attempts = awaitingVersion === null ? 0 : 10;
        for (let attempt = 0; attempt <= attempts; attempt++) {
          const rows: ReportSummary[] = [];
          let after: string | null = null;
          let hasNext = true;
          while (hasNext) {
            const result: ReportsQuery = await graphql<ReportsQuery, { first: number; after: string | null; search: string | null; classification: string | null; inspectionId: string | null }>(ReportsDocument, { first: 100, after, search: null, classification: null, inspectionId });
            if (!active) return;
            rows.push(...result.reports.nodes);
            after = result.reports.pageInfo.endCursor ?? null;
            hasNext = result.reports.pageInfo.hasNextPage && Boolean(after);
          }
          const sorted = rows.sort((a, b) => b.version - a.version);
          if (!active) return;
          setHistory(sorted);
          if (awaitingVersion === null || (sorted[0]?.version ?? 0) >= awaitingVersion) {
            setHistoryMessage("");
            if (awaitingVersion !== null) {
              // Reloading the detail clears the report and re-runs this effect,
              // so the confirmation is tied to that detail request instead of
              // to this effect run.
              const selectedVersion = Number(new URLSearchParams(window.location.search).get("version")) || null;
              const reload = loadDetail(inspectionId, selectedVersion);
              const reloadRequest = detailRequest.current;
              setAwaitingVersion(null);
              await reload;
              if (reloadRequest === detailRequest.current) setDetailMessage(`Versão ${awaitingVersion} disponível no histórico.`);
            }
            return;
          }
          setHistoryMessage("A nova versão está sendo preparada…");
          if (attempt < attempts) {
            await new Promise<void>((resolve) => { timer = window.setTimeout(resolve, 1500); });
            if (!active) return;
          }
        }
        if (active) { setAwaitingVersion(null); setHistoryMessage("A nova versão ainda não apareceu no histórico. Atualize a página para consultar novamente."); }
      } catch { if (active) setHistoryMessage("O histórico de versões não está disponível no momento."); }
    })();
    return () => { active = false; if (timer !== undefined) window.clearTimeout(timer); };
  }, [report?.context.asset.externalKey, inspectionId, awaitingVersion, loadDetail]);

  const closeDialog = () => {
    if (openedFromList.current) {
      router.back();
      return;
    }
    const next = new URLSearchParams(params.toString());
    next.delete("inspectionId");
    router.replace(`/reports${next.size ? `?${next.toString()}` : ""}`, { scroll: false });
  };

  const openDialog = (id: string) => {
    openedFromList.current = true;
    router.push(`/reports?${new URLSearchParams({ ...(search ? { search } : {}), ...(classification ? { classification } : {}), inspectionId: id }).toString()}`, { scroll: false });
  };

  const refreshMedia = () => {
    if (mediaRefreshes.current > 0 || !inspectionId) return;
    mediaRefreshes.current++;
    void loadDetail(inspectionId);
  };

  const prepareDownload = async () => {
    if (!report) return;
    setDetailMessage("Preparando o PDF…");
    setDownload(undefined);
    const request = detailRequest.current;
    const requestedInspection = inspectionId;
    try {
      for (let attempt = 0; attempt < 10; attempt++) {
        const data = await graphql<ReportDownloadQuery, { snapshotId: string }>(ReportDownloadDocument, { snapshotId: report.id });
        if (request !== detailRequest.current || requestedInspection !== inspectionIdRef.current) return;
        const result = data.reportDownload;
        setDownload(result);
        if (result) setReport((current) => current ? { ...current, pdfStatus: result.status } : current);
        if (!result || !["PENDING", "REQUESTED", "PROCESSING"].includes(result.status)) {
          setDetailMessage(result ? `PDF: ${presentReportPDFStatus(result.status)}.` : "O PDF ainda não está disponível.");
          return;
        }
        setDetailMessage(`PDF: ${presentReportPDFStatus(result.status)}. Aguardando a geração…`);
        if (attempt < 9) await new Promise((resolve) => window.setTimeout(resolve, 1500));
      }
      setDetailMessage("O PDF continua em preparação. Você pode tentar novamente em instantes.");
    } catch (error) { setDetailMessage(graphQLFailureText(error)); }
  };

  const updatePublication = async (document: typeof PublishReportDocument | typeof InvalidateReportPublicationDocument, input: Record<string, unknown>): Promise<string | undefined> => {
    const requestedInspection = inspectionId;
    const request = detailRequest.current;
    try {
      const result = await graphql(document as never, { input } as never);
      if (request !== detailRequest.current || requestedInspection !== inspectionIdRef.current) return undefined;
      const payload = Object.values(result as Record<string, unknown>)[0] as { userErrors?: Array<{ message: string; code: string }> };
      const failure = payload.userErrors?.length ? payload.userErrors.map((item) => item.message).join(" · ") : undefined;
      setDetailMessage(failure ?? "Publicação atualizada.");
      if (!payload.userErrors?.length && requestedInspection) await loadDetail(requestedInspection);
      return failure;
    } catch (error) { const failure = graphQLFailureText(error); setDetailMessage(failure); return failure; }
  };

  const currentReport = loadedInspectionId === inspectionId ? report : null;
  return <div className="feature report-page">
    <div className="report-toolbar" role="search">
      <label>Buscar laudo<input type="search" value={searchDraft} onChange={(event) => setSearchDraft(event.target.value)} placeholder="Imóvel, endereço, código ou responsável" /></label>
      <label>Classificação<select value={classification} onChange={(event) => setClassification(event.target.value)}><option value="">Todas</option><option value="NORMAL">Sem alertas identificados</option><option value="ATTENTION">Requer atenção</option><option value="CRITICAL">Crítica</option></select></label>
    </div>
    {listError && <div className="report-list-state" role="alert"><p>{listError}</p><button className="secondary" onClick={() => void loadList(true)}>Tentar novamente</button></div>}{items.length > 0 ? <>
      <p className="report-count">{describeDisplayedReports(items.length, hasMore)}</p>
      <div className="report-table-wrap"><table className="report-table"><thead><tr><th>Imóvel</th><th>Responsável</th><th>Gerado em</th><th>Classificação</th><th>Versão</th><th><span className="visually-hidden">Ação</span></th></tr></thead><tbody>{items.map((item) => <tr key={item.id}>
        <td data-label="Imóvel"><strong>{item.assetName}</strong><span>{item.assetAddress}</span><small>{item.assetExternalKey}</small></td>
        <td data-label="Responsável">{item.participantName}</td><td data-label="Gerado em">{formatGenerated(item.generatedAt)}</td>
        <td data-label="Classificação"><span className={`report-classification report-classification--${item.classification.toLowerCase()}`}>{presentClassification(item.classification)}</span></td>
        <td data-label="Versão">v{item.version}</td><td data-label="Ação" data-align="center" data-action><IconButton label={`Abrir detalhes do laudo de ${item.assetName}`} tooltip="Abrir detalhes" icon="eye" onPress={() => openDialog(item.inspectionId)} /></td>
      </tr>)}</tbody></table></div>
      {hasMore && <button className="secondary report-load-more" onClick={() => void loadList(false)} disabled={listLoading}>{listLoading ? "Carregando…" : "Carregar mais"}</button>}
      {listLoading && <p role="status">Atualizando laudos…</p>}
    </> : shouldShowEmptyReportState(items.length, listError) && <div className="report-list-state report-focus-empty" role="status">{listLoading ? <p>Carregando laudos…</p> : <><span className="report-focus-empty-icon" aria-hidden="true">▧</span><h2>{search || classification ? "Nenhum laudo encontrado" : "Nenhum laudo disponível"}</h2><p>{search || classification ? "Ajuste a busca ou a classificação para consultar outros laudos." : "Os laudos gerados neste contexto ficarão reunidos aqui."}</p><Link href="/inspections">Ver vistorias →</Link></>}</div>}

    <Dialog isOpen={Boolean(inspectionId)} onClose={closeDialog} title="Laudo de vistoria" size="wide">
      <div className="report-dialog-body">
      {detailLoading && <p role="status">Carregando laudo, evidências e constatações…</p>}
      {detailError && <div className="report-detail-state" role="alert"><p>{detailError}</p><button className="secondary" onClick={() => inspectionId && void loadDetail(inspectionId)}>Tentar novamente</button></div>}
      {currentReport && <>
        <ReportVersionHistory history={history} currentVersion={currentReport.version} onSelect={(version) => { const next = new URLSearchParams(params.toString()); next.set("version", String(version)); router.replace(`/reports?${next.toString()}`, { scroll: false }); }} message={historyMessage} canPublish={canPublish} />
        <ReportVisual
          report={currentReport}
          onDownload={() => void prepareDownload()}
          onMediaError={refreshMedia}
          downloadUrl={download?.url ?? undefined}
          downloadMessage={detailMessage}
          onPublish={canPublish ? () => void updatePublication(PublishReportDocument, { inspectionId: currentReport.inspectionId, snapshotId: currentReport.id, clientMutationId: crypto.randomUUID() }) : undefined}
          onInvalidatePublication={canPublish ? () => setInvalidating(true) : undefined}
          onReissue={canCorrectReport && history[0]?.version === currentReport.version ? () => void reissueCurrentReport(currentReport, setDetailMessage, setAwaitingVersion) : undefined}
          canRevisePairing={canCorrectReport && history[0]?.version === currentReport.version}
          actionsDisabled={awaitingVersion !== null}
          onRevisePairing={canCorrectReport && history[0]?.version === currentReport.version ? (itemIdentityId, baselineEvidenceId, currentEvidenceId) => revisePairing(currentReport, itemIdentityId, baselineEvidenceId, currentEvidenceId, setDetailMessage, setAwaitingVersion) : undefined}
        />
      </>}
      </div>
    </Dialog>
    <ConfirmationDialog isOpen={invalidating && Boolean(currentReport)} onClose={() => setInvalidating(false)} title="Invalidar publicação" target="Publicação do laudo selecionado" scope="Contexto operacional atual" consequence="A publicação deixará de estar disponível para o cliente." confirmLabel="Invalidar publicação" reasonLabel="Motivo da invalidação" onConfirm={async (reason) => { if (!currentReport) return; const failure = await updatePublication(InvalidateReportPublicationDocument, { publicationId: currentReport.id, expectedVersion: currentReport.version, reason, clientMutationId: crypto.randomUUID() }); if (failure) throw new Error(failure); }} />
  </div>;
}

async function reissueCurrentReport(report: Report, setMessage: (message: string) => void, setAwaitingVersion: (version: number | null) => void) {
  setMessage("Preparando nova versão do laudo…");
  try {
    const result = await graphql(ReissueReportDocument, { input: { inspectionId: report.inspectionId, expectedVersion: report.version, clientMutationId: crypto.randomUUID() } });
    if (result.reissueReport.userErrors.length) { setMessage(result.reissueReport.userErrors.map((item) => item.message).join(" · ")); return; }
    setAwaitingVersion(report.version + 1);
    setMessage("Nova versão solicitada. Ela ficará disponível após o processamento do laudo.");
  } catch (error) { setMessage(graphQLFailureText(error)); }
}

async function revisePairing(report: Report, itemIdentityId: string, baselineEvidenceId: string, currentEvidenceId: string, setMessage: (message: string) => void, setAwaitingVersion: (version: number | null) => void) {
  setMessage("Salvando revisão do pareamento…");
  try {
    const result = await graphql(ReviseComparisonPairingDocument, { input: { inspectionId: report.inspectionId, itemIdentityId, pairs: [{ baselineEvidenceId, currentEvidenceId }], expectedVersion: report.version, clientMutationId: crypto.randomUUID() } });
    if (result.reviseComparisonPairing.userErrors.length) { setMessage(result.reviseComparisonPairing.userErrors.map((item) => item.message).join(" · ")); return; }
    setAwaitingVersion(report.version + 1);
    setMessage("Nova versão solicitada com o pareamento revisado. Ela ficará disponível após o processamento.");
  } catch (error) { setMessage(graphQLFailureText(error)); }
}

function formatGenerated(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Data indisponível" : new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" }).format(date);
}

export function ReportVersionHistory({ history, currentVersion, onSelect, message, canPublish }: { history: ReportHistoryRow[]; currentVersion: number; onSelect: (version: number) => void; message: string; canPublish: boolean }) {
  return <section className="report-version-history" aria-label="Histórico de versões"><h3>Histórico de versões</h3>{message && <p role="status">{message}</p>}{history.length > 0 && <ol>{history.map((item) => <li key={`${item.id}-${item.version}`}><button type="button" aria-current={item.version === currentVersion ? "page" : undefined} onClick={() => onSelect(item.version)}><strong>Versão {item.version}</strong><span>{item.version === currentVersion ? "Versão selecionada" : item.publication?.status === "PUBLISHED" ? "Versão publicada" : "Versão anterior"}</span><span>{formatGenerated(item.generatedAt)}</span></button><small>{item.scopeLabel} · {item.reviewLabel}</small></li>)}</ol>}{canPublish && <p>Correções geram uma nova versão e preservam as anteriores.</p>}</section>;
}
