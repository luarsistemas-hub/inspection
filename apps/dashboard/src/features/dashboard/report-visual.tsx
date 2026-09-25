import type { CustomerEvidenceQuery, CustomerReportQuery, ReportWorkspaceQuery } from "@/graphql/generated";
import { presentAnalysisMode, presentAnalysisStatus, presentFindingCategory, presentNoRelevantChange, presentClassification } from "./presentation";

type InternalReport = NonNullable<ReportWorkspaceQuery["report"]>;
type CustomerReport = NonNullable<CustomerReportQuery["customerReport"]>;

export function ReportVisual({ report, onDownload, onMediaError }: { report: InternalReport; onDownload: () => void; onMediaError?: () => void }) {
  return <article className="report-visual">
    <ReportHeader classification={report.classification} advisory={report.advisory} context={report.context} version={report.version} />
    <div className="report-visual-actions"><button onClick={onDownload}>Preparar download PDF</button><span>PDF: {report.pdfStatus}</span></div>
    <ReportEvidence requirements={report.requirements} evidence={report.evidence} findings={report.findings} onMediaError={onMediaError} />
  </article>;
}

export function CustomerReportVisual({ report, evidence, onMediaError }: { report: CustomerReport; evidence: CustomerEvidenceQuery["customerEvidence"]["nodes"]; onMediaError?: () => void }) {
  return <article className="report-visual report-visual--customer">
    <ReportHeader classification={report.classification} advisory={report.advisory} context={report.context} version={report.version} />
    <ReportEvidence requirements={report.requirements} evidence={evidence.map((item) => ({ ...item, availability: item.mediaAvailability }))} findings={[]} onMediaError={onMediaError} />
  </article>;
}

function ReportHeader({ classification, advisory, context, version }: { classification: string; advisory: string; context: InternalReport["context"] | CustomerReport["context"]; version: number }) {
  return <header className="report-header">
    <div><p className="report-kicker">Laudo de vistoria · versão {version}</p><h2>{context.asset.name}</h2><p>{context.asset.address}</p><p>{context.asset.externalKey} · Responsável: {context.participant.name}</p></div>
    <div className="report-result"><strong>{presentClassification(classification)}</strong><span>{context.template.name} · v{context.template.version}</span>{context.inspection.stageLabel && <span>{context.inspection.stageLabel}</span>}</div>
    <p className="report-advisory">{advisory}</p>
  </header>;
}

function ReportEvidence({ requirements, evidence, findings, onMediaError }: { requirements: Array<{ key: string; section: string; label: string; instructions?: string | null; noRelevantChange?: boolean | null; analysisMode: string; analysisStatus: string }>; evidence: Array<{ id: string; requirementKey: string; role: string; description?: string | null; capturedAt?: string | null; flags: string[]; availability: string; url?: string | null }>; findings: Array<{ category?: string; title: string; description: string; severity: string; recommendedAction: string; evidenceIds: string[] }>; onMediaError?: () => void }) {
  const unmatched = unmatchedReportEvidence(requirements, evidence);
  const unmatchedReference = unmatched.filter((item) => item.role === "REFERENCE");
  const unmatchedCurrent = unmatched.filter((item) => item.role !== "REFERENCE");
  return <section className="report-requirements" aria-label="Evidências do laudo">
    {requirements.map((requirement) => {
      const items = evidence.filter((item) => item.requirementKey === requirement.key);
      const related = findings.filter((finding) => finding.evidenceIds.some((id) => items.some((item) => item.id === id)));
      const reference = items.filter((item) => item.role === "REFERENCE");
      const current = items.filter((item) => item.role !== "REFERENCE");
      return <section className="report-requirement" key={requirement.key}><header><span>{requirement.section}</span><h3>{requirement.label}</h3>{requirement.instructions && <p>{requirement.instructions}</p>}<small>{presentAnalysisMode(requirement.analysisMode)} · {presentAnalysisStatus(requirement.analysisStatus)}{requirement.analysisMode === "COMPARE_ORIGIN_CURRENT" && requirement.noRelevantChange !== null && ` · ${presentNoRelevantChange(requirement.noRelevantChange)}`}</small></header><div className="report-comparison">{reference.length > 0 && <EvidenceColumn title="Referência" items={reference} onMediaError={onMediaError} />}<EvidenceColumn title={reference.length > 0 ? "Vistoria atual" : "Fotos da vistoria"} items={current} onMediaError={onMediaError} /></div>{related.length > 0 ? <section className="report-findings" aria-label={`Constatações de ${requirement.label}`}>{related.map((finding) => <article key={`${finding.title}-${finding.description}`}><strong>{presentFindingCategory(finding.category)} · {finding.title} · {finding.severity}</strong><p>{finding.description}</p><span>Ação recomendada: {finding.recommendedAction}</span></article>)}</section> : requirement.analysisStatus === "COMPLETED" ? <p className="report-empty">Nenhuma constatação foi registrada; isso não confirma a ausência de problemas.</p> : null}</section>;
    })}
    {unmatched.length > 0 && <section className="report-requirement"><header><span>Evidências</span><h3>Evidências adicionais</h3><p>Estas evidências pertencem ao laudo, mas não estão associadas a um requisito conhecido nesta versão.</p></header><div className="report-comparison">{unmatchedReference.length > 0 && <EvidenceColumn title="Referência" items={unmatchedReference} onMediaError={onMediaError} />}{unmatchedCurrent.length > 0 && <EvidenceColumn title="Fotos da vistoria" items={unmatchedCurrent} onMediaError={onMediaError} />}</div></section>}
  </section>;
}

export function unmatchedReportEvidence<T extends { requirementKey: string }>(requirements: Array<{ key: string }>, evidence: T[]): T[] {
  const requirementKeys = new Set(requirements.map((requirement) => requirement.key));
  return evidence.filter((item) => !requirementKeys.has(item.requirementKey));
}

function EvidenceColumn({ title, items, onMediaError }: { title: string; items: Array<{ id: string; description?: string | null; capturedAt?: string | null; flags: string[]; availability: string; url?: string | null }>; onMediaError?: () => void }) {
  return <section className="report-evidence-column"><h4>{title}</h4>{items.length ? <div className="report-image-grid">{items.map((item) => <figure key={item.id}>{item.availability === "AVAILABLE" && item.url ? <img src={item.url} loading="lazy" alt={item.description ? `${title}: ${item.description}` : title} onError={() => onMediaError?.()} /> : <div className="report-image-missing">Imagem indisponível</div>}<figcaption>{item.description ?? "Sem descrição"}{item.capturedAt && <small>{new Date(item.capturedAt).toLocaleString("pt-BR")}</small>}{item.flags.length > 0 && <small>{item.flags.join(" · ")}</small>}</figcaption></figure>)}</div> : <p className="report-empty">Nenhuma imagem para esta etapa.</p>}</section>;
}
