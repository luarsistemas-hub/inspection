"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { Dialog, IconButton } from "@inspection/design-system";
import { graphql } from "@/graphql/client";
import { InvalidateReportPublicationDocument, PublishReportDocument, ReportDownloadDocument, ReportWorkspaceDocument, ReportsDocument, type ReportDownloadQuery, type ReportWorkspaceQuery, type ReportsQuery } from "@/graphql/generated";
import { presentClassification, presentReportPDFStatus } from "./presentation";
import { ReportVisual } from "./report-visual";
import { ConfirmationDialog } from "./confirmation-dialog";

type ReportSummary = ReportsQuery["reports"]["nodes"][number];
type Report = NonNullable<ReportWorkspaceQuery["report"]>;

/** Prevents a superseded report-list request from replacing the active filters. */
export function isCurrentReportListRequest(request: number, currentRequest: number): boolean {
  return request === currentRequest;
}

export function ReportsJourney({ canPublish, refreshKey = 0 }: { canPublish: boolean; refreshKey?: number }) {
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
      const data = await graphql<ReportsQuery, { first: number; after: string | null; search: string | null; classification: string | null }>(ReportsDocument, { first: 25, after, search: search || null, classification: classification || null });
      if (!isCurrentReportListRequest(request, listRequest.current)) return;
      setItems((current) => reset ? data.reports.nodes : [...current, ...data.reports.nodes]);
      cursorRef.current = data.reports.pageInfo.endCursor ?? null;
      setHasMore(data.reports.pageInfo.hasNextPage);
    } catch (error) {
      if (!isCurrentReportListRequest(request, listRequest.current)) return;
      setListError((error as Error).message || "Não foi possível carregar os laudos.");
    } finally {
      if (isCurrentReportListRequest(request, listRequest.current)) setListLoading(false);
    }
  }, [classification, search]);

  useEffect(() => {
    setItems([]);
    cursorRef.current = null;
    void loadList(true);
  }, [search, classification, refreshKey]);

  const loadDetail = useCallback(async (id: string) => {
    const request = ++detailRequest.current;
    setReport(null);
    setLoadedInspectionId(id);
    setDownload(undefined);
    setDetailMessage("");
    setDetailError("");
    setDetailLoading(true);
    mediaRefreshes.current = 0;
    try {
      const result = await graphql<ReportWorkspaceQuery, { inspectionId: string; version: number | null }>(ReportWorkspaceDocument, { inspectionId: id, version: null });
      if (request !== detailRequest.current) return;
      if (!result.report) setDetailError("Este laudo ainda não está disponível. Atualize a lista e tente novamente.");
      else setReport(result.report);
    } catch (error) {
      if (request === detailRequest.current) setDetailError((error as Error).message || "Não foi possível abrir o laudo.");
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
    void loadDetail(inspectionId);
  }, [inspectionId, loadDetail]);

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
    } catch (error) { setDetailMessage((error as Error).message); }
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
    } catch (error) { const failure = (error as Error).message; setDetailMessage(failure); return failure; }
  };

  const currentReport = loadedInspectionId === inspectionId ? report : null;
  return <div className="feature report-page">
    <div className="report-toolbar" role="search">
      <label>Buscar laudo<input type="search" value={searchDraft} onChange={(event) => setSearchDraft(event.target.value)} placeholder="Imóvel, endereço, código ou responsável" /></label>
      <label>Classificação<select value={classification} onChange={(event) => setClassification(event.target.value)}><option value="">Todas</option><option value="NORMAL">Sem alertas identificados</option><option value="ATTENTION">Requer atenção</option><option value="CRITICAL">Crítica</option></select></label>
    </div>
    {listError ? <div className="report-list-state" role="alert"><p>Não foi possível carregar os laudos. {listError}</p><button className="secondary" onClick={() => void loadList(true)}>Tentar novamente</button></div> : items.length > 0 ? <>
      <div className="report-table-wrap"><table className="report-table"><thead><tr><th>Imóvel</th><th>Responsável</th><th>Gerado em</th><th>Classificação</th><th>Versão</th><th><span className="visually-hidden">Ação</span></th></tr></thead><tbody>{items.map((item) => <tr key={item.id}>
        <td data-label="Imóvel"><strong>{item.assetName}</strong><span>{item.assetAddress}</span><small>{item.assetExternalKey}</small></td>
        <td data-label="Responsável">{item.participantName}</td><td data-label="Gerado em">{formatGenerated(item.generatedAt)}</td>
        <td data-label="Classificação"><span className={`report-classification report-classification--${item.classification.toLowerCase()}`}>{presentClassification(item.classification)}</span></td>
        <td data-label="Versão">v{item.version}</td><td data-label="Ação" data-align="center" data-action><IconButton label={`Abrir detalhes do laudo de ${item.assetName}`} tooltip="Abrir detalhes" icon="eye" onPress={() => openDialog(item.inspectionId)} /></td>
      </tr>)}</tbody></table></div>
      {hasMore && <button className="secondary report-load-more" onClick={() => void loadList(false)} disabled={listLoading}>{listLoading ? "Carregando…" : "Carregar mais"}</button>}
      {listLoading && <p role="status">Atualizando laudos…</p>}
    </> : <p className="report-list-state" role="status">{listLoading ? "Carregando laudos…" : search || classification ? "Nenhum laudo corresponde aos filtros." : "Nenhum laudo gerado neste contexto."}</p>}

    <Dialog isOpen={Boolean(inspectionId)} onClose={closeDialog} title="Laudo de vistoria" size="wide">
      <div className="report-dialog-heading"><span>{currentReport ? `${currentReport.context.asset.name} · v${currentReport.version}` : "Consulta do laudo"}</span><button type="button" className="secondary" onClick={closeDialog} aria-label="Fechar laudo">Fechar</button></div>
      {detailLoading && <p role="status">Carregando laudo, evidências e constatações…</p>}
      {detailError && <div className="report-detail-state" role="alert"><p>{detailError}</p><button className="secondary" onClick={() => inspectionId && void loadDetail(inspectionId)}>Tentar novamente</button></div>}
      {currentReport && <>
        <ReportVisual report={currentReport} onDownload={() => void prepareDownload()} onMediaError={refreshMedia} />
        <p className="report-detail-status" role="status">{detailMessage}{download?.url && <> <a href={download.url} target="_blank" rel="noreferrer">Baixar PDF</a></>}</p>
        {canPublish && <div className="actions"><button onClick={() => void updatePublication(PublishReportDocument, { inspectionId: currentReport.inspectionId, snapshotId: currentReport.id, clientMutationId: crypto.randomUUID() })}>Publicar laudo</button><button className="secondary" onClick={() => setInvalidating(true)}>Invalidar publicação</button></div>}
        <details><summary>Informações técnicas</summary><p>Digest canônico: {currentReport.jsonDigest}</p><p>Digest renderizado: {currentReport.htmlDigest}</p>{download?.sha256 && <p>Digest do PDF: {download.sha256}</p>}<pre>{JSON.stringify(currentReport.canonicalJSON, null, 2)}</pre></details>
      </>}
    </Dialog>
    <ConfirmationDialog isOpen={invalidating && Boolean(currentReport)} onClose={() => setInvalidating(false)} title="Invalidar publicação" target="Publicação do laudo selecionado" scope="Contexto operacional atual" consequence="A publicação deixará de estar disponível para o cliente." confirmLabel="Invalidar publicação" reasonLabel="Motivo da invalidação" onConfirm={async (reason) => { if (!currentReport) return; const failure = await updatePublication(InvalidateReportPublicationDocument, { publicationId: currentReport.id, expectedVersion: currentReport.version, reason, clientMutationId: crypto.randomUUID() }); if (failure) throw new Error(failure); }} />
  </div>;
}

function formatGenerated(value: string) {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Data indisponível" : new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" }).format(date);
}
