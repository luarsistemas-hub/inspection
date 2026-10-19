import { useEffect, useMemo, useRef, useState } from "react";
import { Icon } from "@inspection/design-system";
import type { CustomerEvidenceQuery, CustomerReportQuery, ReportWorkspaceQuery } from "@/graphql/generated";
import { getAccessToken, getMembershipId } from "@/auth/session";
import { prepareVideoPlaybackSession } from "@/auth/video-playback-session";
import { presentAnalysisStatus, presentFindingCategory, presentClassification, presentNoRelevantChange } from "./presentation";

type InternalReport = NonNullable<ReportWorkspaceQuery["report"]>;
type CustomerReport = NonNullable<CustomerReportQuery["customerReport"]>;

export function ReportVisual({ report, onDownload, onMediaError, downloadUrl, downloadMessage, onPublish, onInvalidatePublication }: {
  report: InternalReport;
  onDownload: () => void;
  onMediaError?: () => void;
  downloadUrl?: string;
  downloadMessage?: string;
  onPublish?: () => void;
  onInvalidatePublication?: () => void;
}) {
  return <article className="report-visual">
    <header className="report-header report-header--internal">
      <div className="report-property">
        <p className="report-address">{report.context.asset.address || report.context.asset.name}</p>
        <p className="report-version">Versão {report.version}</p>
      </div>
      <div className="report-header-bottom">
        <div className="report-status-line">
          <strong className={`report-classification report-classification--${report.classification.toLowerCase()}`}>{presentClassification(report.classification)}</strong>
          <details className="report-advisory-info"><summary aria-label="Sobre a triagem interna"><Icon name="info" size={18} /></summary><p>{report.advisory}</p></details>
        </div>
        <div className="report-header-tools">
          {downloadUrl ? <a className="report-download-link" href={downloadUrl} target="_blank" rel="noreferrer"><span className="report-download-full">Baixar PDF</span><span className="report-download-short">PDF</span></a> : <button className="report-download-button" onClick={onDownload} aria-label="Preparar PDF"><span className="report-download-full">Preparar PDF</span><span className="report-download-short">PDF</span></button>}
          {onPublish && onInvalidatePublication && <details className="report-publication-menu"><summary aria-label="Gerenciar publicação"><Icon name="more-horizontal" size={20} /></summary><div><span>Gestão da publicação</span><button type="button" onClick={onPublish}>Publicar para o cliente</button><button type="button" onClick={onInvalidatePublication}>Invalidar publicação</button></div></details>}
        </div>
      </div>
      {downloadMessage && <p className="report-detail-status" role="status">{downloadMessage}</p>}
    </header>
    <div className="report-details">
      <details><summary>Dados do imóvel e da vistoria</summary><p>{report.context.asset.externalKey} · Responsável: {report.context.participant?.name}<br />{report.context.template?.name} · v{report.context.template?.version}{report.context.inspection?.stageLabel && <> · {report.context.inspection.stageLabel}</>}</p></details>
    </div>
    <ReportSections key={report.id} requirements={report.requirements} evidence={report.evidence.nodes} findings={report.findings} onMediaError={onMediaError} />
  </article>;
}

export function CustomerReportVisual({ report, evidence, onMediaError }: { report: CustomerReport; evidence: CustomerEvidenceQuery["customerEvidence"]["nodes"]; onMediaError?: () => void }) {
  return <article className="report-visual report-visual--customer">
    <CustomerReportHeader classification={report.classification} advisory={report.advisory} context={report.context} version={report.version} />
    <ReportEvidence requirements={report.requirements} evidence={evidence.map((item) => ({ ...item, availability: item.mediaAvailability }))} findings={[]} onMediaError={onMediaError} />
  </article>;
}

function CustomerReportHeader({ classification, advisory, context, version }: { classification: string; advisory: string; context: CustomerReport["context"]; version: number }) {
  return <header className="report-header">
    <div><p className="report-kicker">Laudo de vistoria · versão {version}</p><h2>{context.asset.name}</h2><p>{context.asset.address}</p><p>{context.asset.externalKey} · Responsável: {context.participant.name}</p></div>
    <div className="report-result"><strong>{presentClassification(classification)}</strong><span>{context.template.name} · v{context.template.version}</span>{context.inspection.stageLabel && <span>{context.inspection.stageLabel}</span>}</div>
    <p className="report-advisory">{advisory}</p>
  </header>;
}

const UNMATCHED_SECTION = "\u0000unmatched";
const UNMATCHED_LABEL = "Evidências adicionais";

function ReportSections({ requirements, evidence, findings, onMediaError }: { requirements: InternalReport["requirements"]; evidence: InternalReport["evidence"]["nodes"]; findings: InternalReport["findings"]; onMediaError?: () => void }) {
  const { sections, counts, unmatched } = useMemo(() => {
    const names = [...new Set(requirements.map((requirement) => requirement.section))];
    const unmatchedItems = unmatchedReportEvidence(requirements, evidence);
    const countBySection = new Map<string, number>();
    for (const requirement of requirements) countBySection.set(requirement.section, (countBySection.get(requirement.section) ?? 0) + 1);
    if (unmatchedItems.length) countBySection.set(UNMATCHED_SECTION, unmatchedItems.length);
    return { sections: unmatchedItems.length ? [...names, UNMATCHED_SECTION] : names, counts: countBySection, unmatched: unmatchedItems };
  }, [requirements, evidence]);
  const [requestedSection, setRequestedSection] = useState(sections[0] ?? "");
  const activeSection = sections.includes(requestedSection) ? requestedSection : sections[0] ?? "";
  const showUnmatched = activeSection === UNMATCHED_SECTION;
  const visibleRequirements = useMemo(() => requirements.filter((requirement) => requirement.section === activeSection), [requirements, activeSection]);
  const visibleEvidence = useMemo(() => {
    if (showUnmatched) return unmatched;
    const keys = new Set(visibleRequirements.map((requirement) => requirement.key));
    return evidence.filter((item) => keys.has(item.requirementKey));
  }, [showUnmatched, unmatched, visibleRequirements, evidence]);

  if (!sections.length) return <ReportEvidence requirements={requirements} evidence={evidence} findings={findings} onMediaError={onMediaError} />;

  return <div className="report-workspace">
    {sections.length > 1 && <nav className="report-section-nav" aria-label="Ambientes do laudo">
      <span>Neste laudo</span>
      {sections.map((section) => <button type="button" key={section} aria-pressed={section === activeSection} onClick={() => setRequestedSection(section)}>{section === UNMATCHED_SECTION ? UNMATCHED_LABEL : section}<small>{counts.get(section) ?? 0}</small></button>)}
    </nav>}
    <div className="report-section-content">
      {showUnmatched
        ? <ReportEvidence requirements={[]} evidence={visibleEvidence} findings={[]} onMediaError={onMediaError} />
        : <ReportEvidence requirements={visibleRequirements} evidence={visibleEvidence} findings={findings} onMediaError={onMediaError} />}
    </div>
  </div>;
}

type ReportEvidenceItem = { id: string; requirementKey: string; role: string; description?: string | null; capturedAt?: string | null; flags: string[]; availability: string; sourceAvailability?: string | null; url?: string | null; evidenceKind?: string | null; frameId?: string | null; timestampMs?: number | null; ptsMicros?: string | null; sourceMediaId?: string | null; component?: string | null; label?: string | null };

function ReportEvidence({ requirements, evidence, findings, onMediaError }: { requirements: Array<{ key: string; section: string; label: string; instructions?: string | null; noRelevantChange?: boolean | null; analysisMode: string; analysisStatus: string }>; evidence: ReportEvidenceItem[]; findings: Array<{ category?: string; title: string; description: string; severity: string; recommendedAction: string; evidenceIds: string[] }>; onMediaError?: () => void }) {
  const unmatched = unmatchedReportEvidence(requirements, evidence);
  const unmatchedReference = unmatched.filter((item) => item.role === "REFERENCE");
  const unmatchedCurrent = unmatched.filter((item) => item.role !== "REFERENCE");
  return <section className="report-requirements" aria-label="Evidências do laudo">
    {requirements.map((requirement) => {
      const items = evidence.filter((item) => item.requirementKey === requirement.key);
      const related = findings.filter((finding) => finding.evidenceIds.some((id) => items.some((item) => item.id === id)));
      const reference = items.filter((item) => item.role === "REFERENCE");
      const current = items.filter((item) => item.role !== "REFERENCE");
      return <section className="report-requirement" key={requirement.key}>
        <header className="report-requirement-heading"><div><span>{requirement.section}</span><h3>{requirement.label}</h3>{requirement.instructions && <p>{requirement.instructions}</p>}</div><small>{presentAnalysisStatus(requirement.analysisStatus)}{related.length === 0 && requirement.analysisMode === "COMPARE_ORIGIN_CURRENT" && requirement.noRelevantChange !== null && <> · {presentNoRelevantChange(requirement.noRelevantChange)}</>}</small></header>
        <div className={`report-requirement-body${related.length ? "" : " report-requirement-body--without-findings"}`}>
          {requirement.analysisMode === "CURRENT_ONLY" && <p className="report-no-baseline">Esta análise mostra apenas a condição observada agora; não há referência histórica para comparar.</p>}
          {related.length > 0 && <section className="report-findings" aria-label={`Constatações de ${requirement.label}`}>{related.map((finding) => <article key={`${finding.title}-${finding.description}`}><span>{presentFindingCategory(finding.category)} · {finding.severity}</span><h4>{finding.title}</h4><p>{finding.description}</p><p><strong>Ação recomendada</strong> · {finding.recommendedAction}</p></article>)}</section>}
          <div className="report-comparison">{reference.length > 0 && <EvidenceColumn title="Referência" items={reference} onMediaError={onMediaError} />}<EvidenceColumn title={reference.length > 0 ? "Vistoria atual" : "Fotos da vistoria"} items={current} onMediaError={onMediaError} /></div>
          {related.length === 0 && requirement.analysisStatus === "COMPLETED" && <p className="report-no-findings">Nenhuma constatação foi registrada; isso não confirma a ausência de problemas.</p>}
        </div>
      </section>;
    })}
    {unmatched.length > 0 && <section className="report-requirement">
      <header className="report-requirement-heading"><div><span>Evidências</span><h3>Evidências adicionais</h3><p>Estas evidências pertencem ao laudo, mas não estão associadas a um requisito conhecido nesta versão.</p></div></header>
      <div className="report-requirement-body report-requirement-body--without-findings"><div className="report-comparison">{unmatchedReference.length > 0 && <EvidenceColumn title="Referência" items={unmatchedReference} onMediaError={onMediaError} />}{unmatchedCurrent.length > 0 && <EvidenceColumn title="Fotos da vistoria" items={unmatchedCurrent} onMediaError={onMediaError} />}</div></div>
    </section>}
  </section>;
}

export function unmatchedReportEvidence<T extends { requirementKey: string }>(requirements: Array<{ key: string }>, evidence: T[]): T[] {
  const requirementKeys = new Set(requirements.map((requirement) => requirement.key));
  return evidence.filter((item) => !requirementKeys.has(item.requirementKey));
}

function EvidenceColumn({ title, items, onMediaError }: { title: string; items: ReportEvidenceItem[]; onMediaError?: () => void }) {
  const videoEvidence = items.some(isVideoEvidence);
  return <section className="report-evidence-column"><h4>{videoEvidence ? (title === "Referência" ? "ORIGEM" : "ATUAL") : title}</h4>{items.length ? <div className="report-image-grid">{items.map((item) => <figure key={`${item.id}:${item.url ?? ""}`}>{item.availability === "AVAILABLE" && item.url ? <EvidenceImage url={item.url} alt={item.description ? `${title}: ${item.description}` : title} onMediaError={onMediaError} /> : <div className="report-image-missing">Imagem indisponível</div>}{isVideoEvidence(item) && <VideoEvidencePlayer item={item} onMediaError={onMediaError} /> }<figcaption>{item.description ?? item.label ?? "Sem descrição"}{(item.component || item.timestampMs !== null && item.timestampMs !== undefined || item.frameId) && <small>{[item.component, formatVideoTime(item.timestampMs), item.frameId ? `Frame ${shortFrameId(item.frameId)}` : undefined].filter(Boolean).join(" · ")}</small>}{item.ptsMicros && <small>PTS {formatVideoTime(Number(item.ptsMicros) / 1000)}</small>}{item.capturedAt && <small>{new Date(item.capturedAt).toLocaleString("pt-BR")}</small>}{(item.sourceAvailability === "EXPIRED" || item.sourceAvailability === "MISSING") && isVideoEvidence(item) && <small>Vídeo original expirado; frame preservado.</small>}{item.flags.length > 0 && <small>{item.flags.join(" · ")}</small>}</figcaption></figure>)}</div> : <p className="report-empty">Nenhuma imagem para esta etapa.</p>}</section>;
}

function isVideoEvidence(item: ReportEvidenceItem): boolean { return item.evidenceKind === "VIDEO_FRAME" && Boolean(item.sourceMediaId); }
function formatVideoTime(timestampMs?: number | null): string | undefined {
  if (timestampMs === null || timestampMs === undefined || !Number.isFinite(timestampMs) || timestampMs < 0) return undefined;
  const seconds = Math.floor(timestampMs / 1000);
  return `${String(Math.floor(seconds / 60)).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`;
}
function shortFrameId(frameId: string): string { return frameId.slice(0, 8); }

export function VideoEvidencePlayer({ item, onMediaError }: { item: ReportEvidenceItem; onMediaError?: () => void }) {
  const [active, setActive] = useState(false);
  const [state, setState] = useState<"idle" | "preparing" | "playing" | "unavailable" | "unsupported">("idle");
  const [sessionReady, setSessionReady] = useState(false);
  const [seekUnavailable, setSeekUnavailable] = useState(false);
  const videoRef = useRef<HTMLVideoElement>(null);
  const expired = item.sourceAvailability === "EXPIRED" || item.sourceAvailability === "MISSING";
  const timestampMs = item.timestampMs;
  const validTimestamp = timestampMs !== null && timestampMs !== undefined && Number.isFinite(timestampMs) && timestampMs >= 0;
  const endpoint = item.sourceMediaId ? `/api/media/videos/${encodeURIComponent(item.sourceMediaId)}/content` : "";

  const activate = async () => {
    setActive(true);
    if (expired) { setState("unavailable"); return; }
    if (!validTimestamp || !endpoint) { setSeekUnavailable(true); setState("unavailable"); return; }
    setState("preparing");
    try {
      await prepareVideoPlaybackSession();
      setSessionReady(true);
      setState("playing");
    } catch {
      setState("unavailable");
      onMediaError?.();
    }
  };

  const seekToEvidence = () => {
    const video = videoRef.current;
    if (!video || !validTimestamp || timestampMs === null || timestampMs === undefined) return;
    if (!Number.isFinite(video.duration) || timestampMs / 1000 >= video.duration) { setSeekUnavailable(true); setState("unavailable"); return; }
    video.currentTime = timestampMs / 1000;
  };

  return <div className="report-video-player">
    {!expired && !active && <button type="button" className="report-video-activate" onClick={() => void activate()} aria-label={`Reproduzir vídeo no ponto ${formatVideoTime(timestampMs) ?? "indisponível"}`}>Reproduzir vídeo · {formatVideoTime(timestampMs) ?? "tempo indisponível"}</button>}
    {expired && <p role="status">Vídeo original expirado; o frame retido continua disponível.</p>}
    {active && seekUnavailable && <p role="status">Evidência de vídeo indisponível neste timestamp.</p>}
    {active && state === "preparing" && <p role="status">Preparando reprodução…</p>}
    {active && state === "unavailable" && !expired && !seekUnavailable && <p role="status">Não foi possível carregar o vídeo. O frame retido continua disponível.</p>}
    {active && sessionReady && !expired && !seekUnavailable && validTimestamp && endpoint && <div className="report-video-controls"><video ref={videoRef} src={endpoint} preload="metadata" controls playsInline aria-label={`Vídeo da evidência ${item.label ?? item.component ?? shortFrameId(item.frameId ?? item.id)}`} onLoadedMetadata={seekToEvidence} onError={() => { setState("unsupported"); onMediaError?.(); }} /><button type="button" onClick={seekToEvidence}>Ir para {formatVideoTime(timestampMs)}</button>{state === "unsupported" && <p role="status">Este navegador não reproduz o vídeo. As imagens e constatações continuam disponíveis.</p>}</div>}
  </div>;
}

function EvidenceImage({ url, alt, onMediaError }: { url: string; alt: string; onMediaError?: () => void }) {
  const [blobURL, setBlobURL] = useState<string>();
  const onError = useRef(onMediaError);
  useEffect(() => { onError.current = onMediaError; }, [onMediaError]);
  const authenticatedFrame = url.startsWith("/media/video-evidence/");
  useEffect(() => {
    if (!authenticatedFrame) return;
    const controller = new AbortController();
    let createdURL: string | undefined;
    setBlobURL(undefined);
    const load = async () => {
      const token = getAccessToken();
      if (!token) throw new Error("missing access token");
      const endpoint = new URL(process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql");
      const target = new URL(url, endpoint.origin);
      const membership = getMembershipId();
      const response = await fetch(target, { signal: controller.signal, credentials: "omit", headers: { Authorization: `Bearer ${token}`, ...(membership ? { "X-Inspection-Membership-ID": membership } : {}) } });
      if (!response.ok) throw new Error("frame unavailable");
      const blob = await response.blob();
      if (!blob.type.startsWith("image/")) throw new Error("invalid frame content");
      createdURL = URL.createObjectURL(blob);
      if (!controller.signal.aborted) setBlobURL(createdURL);
    };
    void load().catch(() => { if (!controller.signal.aborted) onError.current?.(); });
    return () => { controller.abort(); if (createdURL) URL.revokeObjectURL(createdURL); };
  }, [authenticatedFrame, url]);
  if (authenticatedFrame && !blobURL) return <div className="report-image-missing">Carregando frame…</div>;
  return <img src={authenticatedFrame ? blobURL : url} loading="lazy" alt={alt} onError={() => onMediaError?.()} />;
}
