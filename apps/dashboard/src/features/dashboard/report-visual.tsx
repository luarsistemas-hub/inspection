import { useEffect, useId, useMemo, useRef, useState } from "react";
import { Icon } from "@inspection/design-system";
import type { CustomerEvidenceQuery, CustomerReportQuery, ReportWorkspaceQuery } from "@/graphql/generated";
import { accessTokenForRequest, getMembershipId } from "@/auth/session";
import { prepareVideoPlaybackSession } from "@/auth/video-playback-session";
import { buildReportRooms, presentReportLabel, presentReportSection, type ReportRoom, type RoomItem } from "./report-room-model";
import { presentAnalysisStatus, presentFindingCategory, presentFindingSeverity, presentNoRelevantChange } from "./presentation";
import { buildCoverageSummary, comparisonOutcomeLabel, conditionLabel, inventoryEntryStatusLabel, itemTypeLabel, noBasisLabel, purposeLabel, reportConditionLabel } from "./inventory-presentation";

type InternalReport = NonNullable<ReportWorkspaceQuery["report"]>;
type CustomerReport = NonNullable<CustomerReportQuery["customerReport"]>;

export function ReportVisual({ report, onDownload, onMediaError, downloadUrl, downloadMessage, onPublish, onInvalidatePublication, onReissue, onRevisePairing, canRevisePairing = false, actionsDisabled = false }: {
  report: InternalReport;
  onDownload: () => void;
  onMediaError?: () => void;
  downloadUrl?: string;
  downloadMessage?: string;
  onPublish?: () => void;
  onInvalidatePublication?: () => void;
  onReissue?: () => void;
  onRevisePairing?: (itemIdentityId: string, baselineEvidenceId: string, currentEvidenceId: string) => Promise<void>;
  canRevisePairing?: boolean;
  actionsDisabled?: boolean;
}) {
  const { rooms, captureInstructions } = useMemo(() => buildReportRooms(report), [report]);
  const [inventoryLayout, setInventoryLayout] = useState<"cards" | "list">("cards");
  const classificationLabel = report.classificationLabel;
  return <article className="report-visual">
    <header className="report-header report-header--internal">
      <div className="report-property">
        <h2 className="report-address">{report.context.asset.address || report.context.asset.name}</h2>
        <p className="report-version">Análise por ambiente · Versão {report.version}</p>
      </div>
      <div className="report-header-bottom">
        <div className="report-status-line">
          <span className={`report-classification${report.classification === "CRITICAL" ? " report-classification--critical" : report.classification === "ATTENTION" ? " report-classification--attention" : ""}`}>{classificationLabel}</span>
          <ReportCounts rooms={rooms} findingCount={report.findings.length} />
          <details className="report-advisory-info"><summary aria-label="Sobre a triagem interna"><Icon name="info" size={18} /></summary><p>{classificationLabel}. {report.advisory}</p></details>
        </div>
        <div className="report-header-tools">
          {downloadUrl ? <a className="report-download-link" href={downloadUrl} target="_blank" rel="noreferrer"><span className="report-download-full">Baixar PDF</span><span className="report-download-short">PDF</span></a> : <button className="report-download-button" onClick={onDownload} aria-label="Preparar PDF"><span className="report-download-full">Preparar PDF</span><span className="report-download-short">PDF</span></button>}
          {onPublish && onInvalidatePublication && <details className="report-publication-menu"><summary aria-label="Gerenciar publicação"><Icon name="more-horizontal" size={20} /></summary><div><span>Gestão da publicação</span><button type="button" onClick={onPublish}>Publicar para o cliente</button><button type="button" onClick={onInvalidatePublication}>Invalidar publicação</button></div></details>}
          {onReissue && <button type="button" className="secondary" disabled={actionsDisabled} onClick={onReissue}>Emitir nova versão</button>}
        </div>
      </div>
      {downloadMessage && <p className="report-detail-status" role="status">{downloadMessage}</p>}
    </header>
    <section className="report-executive-summary" aria-label="Identificação e síntese do laudo"><h3>Identificação e síntese</h3><p>Finalidade: {report.context.inspection?.stageLabel || "Laudo de vistoria"} · Data declarada: {report.context.inspection?.submittedAt ? formatReportDate(report.context.inspection.submittedAt) : "não informada"} · Emissão: {formatReportDate(report.createdAt)} · Versão {report.version}</p><p>Perfil de bens: {report.scopeProfile || "Não informado"}{report.processing ? ` · Análise: ${presentAnalysisStatus(report.processing.status)}` : ""}</p>{report.checklist && <p>{coverageLabel(report.checklist.coverage, report.checklist.rooms.length)}</p>}<p>{findingCountsSummary(report.findingCounts)} · {report.handover?.entries.filter((entry) => entry.status === "PENDING").length ?? 0} registro(s) de entrega pendente(s)</p></section>
    <div className="report-details">
      <details><summary>Dados do imóvel e da vistoria</summary><p>{report.context.asset.externalKey} · Responsável: {report.context.participant?.name}<br />{report.context.template?.name} · v{report.context.template?.version}{report.context.inspection?.stageLabel && <> · {report.context.inspection.stageLabel}</>}</p>{captureInstructions.map((instruction) => <p key={instruction}>{instruction}</p>)}</details>
    </div>
    {report.evidenceNotice && <p role="note">{report.evidenceNotice}</p>}{((report.scopeStatements?.length ?? 0) > 0 || (report.reasonLabels?.length ?? 0) > 0) && <ReportReasons scope={report.scopeStatements ?? []} reasons={report.reasonLabels ?? []} />}
    {report.checklist ? <DescriptiveChecklist checklist={report.checklist} evidence={report.evidence.nodes} snapshotId={report.id} onMediaError={onMediaError} /> : report.inventory && <InventoryReport inventory={report.inventory} evidence={report.evidence.nodes} snapshotId={report.id} classification={report.classification} limited={report.limited} layout={inventoryLayout} onLayoutChange={setInventoryLayout} collapseNotObserved />}
    {report.processing && <ProcessingState status={report.processing.status} message={report.processing.message} />}
    {report.handover && <HandoverReport entries={report.handover.entries} />}
    {report.comparison && <ComparisonReport comparison={report.comparison} evidence={report.evidence.nodes} onMediaError={onMediaError} canRevisePairing={canRevisePairing} onRevisePairing={onRevisePairing} actionsDisabled={actionsDisabled} />}
    {(report.requirements.length > 0 || report.findings.length > 0 || !report.checklist) && !(report.inventory && !report.checklist && !report.comparison && report.classification === "NORMAL" && !report.limited && report.inventory.problems.length === 0) && <ReportSections key={report.id} rooms={rooms} onMediaError={onMediaError} />}
  </article>;
}

export function CustomerReportVisual({ report, evidence, onMediaError }: { report: CustomerReport; evidence: CustomerEvidenceQuery["customerEvidence"]["nodes"]; onMediaError?: () => void }) {
  const classificationLabel = report.classificationLabel;
  const customerEvidenceNotice = report.evidenceNotice;
  return <article className="report-visual report-visual--customer">
    <CustomerReportHeader classificationLabel={classificationLabel} advisory={report.advisory} context={report.context} version={report.version} historical={report.historical} showVersionState={Boolean(report.checklist || report.comparison)} />
    <section className="report-executive-summary" aria-label="Identificação e síntese do laudo"><h3>Identificação e síntese</h3><p>Finalidade: {report.context.inspection.stageLabel || "Laudo de vistoria"} · Data declarada: {report.context.inspection.submittedAt ? formatReportDate(report.context.inspection.submittedAt) : "não informada"} · Emissão: {formatReportDate(report.context.inspection.generatedAt)} · Versão {report.version}</p><p>Perfil de bens: {report.scopeProfile || "Não informado"}</p>{report.checklist && <p>{coverageLabel(report.checklist.coverage, report.checklist.rooms.length)}</p>}<p>{findingCountsSummary(report.findingCounts)} · {report.handover?.entries.filter((entry) => entry.status === "PENDING").length ?? 0} registro(s) de entrega pendente(s){report.comparison ? ` · ${report.comparison.counts.total} itens na comparação` : ""}</p></section>
    {customerEvidenceNotice && <p role="note">{customerEvidenceNotice}</p>}
    {((report.scopeStatements?.length ?? 0) > 0 || (report.reasonLabels?.length ?? 0) > 0) && <ReportReasons scope={report.scopeStatements ?? []} reasons={report.reasonLabels ?? []} />}
    {report.checklist ? <CustomerChecklistView checklist={report.checklist} evidence={evidence} snapshotId={report.snapshotId} onMediaError={onMediaError} /> : report.inventory && <InventoryReport inventory={report.inventory} evidence={evidence.map((item) => ({ ...item, availability: item.mediaAvailability }))} snapshotId={report.snapshotId} classification={report.classification} limited={report.limited} />}
    {report.processing && <ProcessingState status={report.processing.status} message={report.processing.message} />}
    {report.handover && <CustomerHandoverView entries={report.handover.entries} />}
    {report.comparison && <CustomerComparisonView comparison={report.comparison} evidence={evidence.map((item) => ({ id: item.id, snapshotId: report.snapshotId, requirementKey: item.requirementKey, role: item.role, description: item.description, capturedAt: item.capturedAt, flags: item.flags, availability: item.mediaAvailability ?? item.state, sourceAvailability: item.sourceAvailability, url: item.url, evidenceKind: item.evidenceKind, frameId: item.frameId, timestampMs: item.timestampMs, ptsMicros: item.ptsMicros, sourceMediaId: item.sourceMediaId, component: item.component, label: item.label, reference: item.reference }))} onMediaError={onMediaError} />}
    {(report.requirements.length > 0 || !report.checklist) && !(report.inventory && !report.checklist && !report.comparison && report.classification === "NORMAL" && !report.limited && report.inventory.problems.length === 0) && <ReportEvidence requirements={report.requirements} evidence={evidence.map((item) => ({ ...item, snapshotId: report.snapshotId, availability: item.mediaAvailability }))} findings={[]} hasReferenceInReport={evidence.some((item) => item.role === "REFERENCE")} onMediaError={onMediaError} />}
  </article>;
}

function coverageLabel(coverage: { expected: number; expectedRooms?: number | null; fullyAssessed: number; partiallyAssessed: number; notObserved: number; notApplicable: number; impossible: number; unknownScope: boolean }, registeredRooms?: number) {
  const roomSummary = `${coverage.expectedRooms ?? "Ambientes previstos não informados"} · ${registeredRooms ?? 0} ambiente(s) registrado(s)`;
  if (coverage.unknownScope || coverage.expected === 0) return `${roomSummary}. Escopo não definido; não há percentual de cobertura.`;
  return `${roomSummary} · avaliação completa: ${coverage.fullyAssessed} de ${coverage.expected} componentes · avaliação parcial: ${coverage.partiallyAssessed} · não observados: ${coverage.notObserved} · não aplicáveis: ${coverage.notApplicable} · impossíveis de capturar: ${coverage.impossible}`;
}
function findingCountsSummary(value?: { total: number; critical: number; high: number; medium: number; low: number; informational: number }) {
  const counts = value ?? { total: 0, critical: 0, high: 0, medium: 0, low: 0, informational: 0 };
  if (counts.total === 0) return "Nenhuma constatação registrada";
  const details = [["críticas", counts.critical], ["altas", counts.high], ["moderadas", counts.medium], ["baixas", counts.low], ["informativas", counts.informational]].filter(([, count]) => Number(count) > 0).map(([label, count]) => `${count} ${label}`);
  return `${counts.total} constatação(ões) (${details.join(" · ")})`;
}
function comparisonCountLabel(displayed: number, total: number) { return `${displayed} de ${total} ${total === 1 ? "item" : "itens"}`; }

function componentState(value: string) {
  return ({ OBSERVED: "Observado", PARTIAL: "Parcial", NOT_OBSERVED: "Não observado", NOT_APPLICABLE: "Não aplicável", IMPOSSIBLE: "Impossível de capturar" } as Record<string, string>)[value] ?? "Situação não informada";
}
function checklistGroup(typeCode: string | null | undefined) {
  if (["WALL", "FLOOR", "CEILING", "ROOF"].includes(typeCode ?? "")) return "Superfícies";
  if (["DOOR", "WINDOW"].includes(typeCode ?? "")) return "Aberturas";
  if (["CABINET", "WARDROBE", "COUNTERTOP"].includes(typeCode ?? "")) return "Marcenaria e bancadas";
  if (["SINK", "FAUCET", "TOILET", "SHOWER"].includes(typeCode ?? "")) return "Instalações hidráulicas";
  if (["APPLIANCE", "APPLIANCE_OTHER", "LIGHTING", "SOCKET_SWITCH"].includes(typeCode ?? "")) return "Instalações e equipamentos";
  if (["FURNITURE", "SOFA", "TABLE", "CHAIR"].includes(typeCode ?? "")) return "Mobiliário";
  return "Outros componentes";
}
function groupChecklist<T>(items: T[], getGroup: (item: T) => string): Array<{ group: string; items: T[] }> {
  const groups = new Map<string, T[]>();
  for (const item of items) { const key = getGroup(item); groups.set(key, [...(groups.get(key) ?? []), item]); }
  const order = ["Superfícies", "Aberturas", "Marcenaria e bancadas", "Instalações hidráulicas", "Instalações e equipamentos", "Mobiliário", "Outros componentes"];
  return [...groups].map(([group, items]) => ({ group, items })).sort((a, b) => (order.indexOf(a.group) < 0 ? order.length : order.indexOf(a.group)) - (order.indexOf(b.group) < 0 ? order.length : order.indexOf(b.group)));
}
function DescriptiveChecklist({ checklist, evidence, snapshotId, onMediaError }: { checklist: NonNullable<InternalReport["checklist"]>; evidence: ReportEvidenceItem[]; snapshotId: string; onMediaError?: () => void }) {
  const evidenceById = new Map(evidence.map((item) => [item.id, item]));
  return <section className="report-descriptive-checklist" aria-label="Checklist descritivo">
    <header><h3>Checklist descritivo</h3></header>
    {checklist.rooms.map((room) => <section key={room.key} className="report-checklist-room"><h4>{room.name}</h4>{room.components.length === 0 ? <p>Nenhuma evidência registrada; isso não significa ausência de problemas.</p> : groupChecklist(room.components, (component) => checklistGroup(component.typeCode)).map(({ group, items }) => <div className="report-checklist-group" key={group}><h5>{group}</h5><ul>{items.map((component, index) => <li key={`${component.key}-${index}`}>
      <strong>{component.label}</strong><span>{componentState(component.status)}</span>
      {component.description && <p>{component.description}</p>}{component.condition && <p>Condição observada: {reportConditionLabel(component.condition)}</p>}
      {component.reviewLabel && <p>{component.reviewLabel}</p>}
      {component.assessedScope && <p>Extensão avaliada: {component.assessedScope}</p>}
      {component.notAssessed.length > 0 && <p>Partes não avaliadas: {component.notAssessed.join(", ")}</p>}
      {component.limitations.map((limitation, limitationIndex) => <p key={limitationIndex}>Limitação: {limitation}</p>)}
      {component.conflict && <p>Há informações divergentes que ainda precisam de análise.</p>}
      {component.outsideScope && <p>Observado fora do escopo; não compõe a cobertura nem o inventário de referência.</p>}
      {(component.attributeSources ?? []).map((source, sourceIndex) => <p key={sourceIndex}>{source}</p>)}
      {component.evidenceIds.length > 0 && <div className="report-checklist-evidence"><strong>Evidências</strong>{component.evidenceIds.map((id) => { const item = evidenceById.get(id); return <figure key={id}>{item && (item.evidenceKind === "VIDEO_FRAME" ? <VideoEvidencePlayer item={{ ...item, snapshotId }} onMediaError={onMediaError} /> : <EvidenceThumbnail item={item} onMediaError={onMediaError} />)}<figcaption>{item?.reference ?? item?.description ?? "Evidência disponível no laudo"}</figcaption></figure>; })}</div>}
    </li>)}</ul></div>)}</section>)}
    {!checklist.rooms.some((room) => room.components.length > 0) && <p>Nenhum componente observado.</p>}
  </section>;
}

export function CustomerChecklistView({ checklist, evidence, snapshotId, onMediaError }: { checklist: NonNullable<NonNullable<CustomerReport["checklist"]>>; evidence: CustomerEvidenceQuery["customerEvidence"]["nodes"]; snapshotId: string; onMediaError?: () => void }) {
  const evidenceByReference = new Map<string, ReportEvidenceItem>(evidence.map((item) => [item.reference, { id: item.id, snapshotId, requirementKey: item.requirementKey, role: item.role, description: item.description, capturedAt: item.capturedAt, flags: item.flags, availability: item.mediaAvailability ?? item.state, sourceAvailability: item.sourceAvailability, url: item.url, evidenceKind: item.evidenceKind, frameId: item.frameId, timestampMs: item.timestampMs, ptsMicros: item.ptsMicros, sourceMediaId: item.sourceMediaId, component: item.component, label: item.label, reference: item.reference }]));
  return <section className="report-descriptive-checklist" aria-label="Checklist descritivo"><header><h3>Checklist descritivo</h3></header>
    {checklist.rooms.map((room, roomIndex) => <section key={room.name + "-" + roomIndex} className="report-checklist-room"><h4>{room.name}</h4>{room.components.length === 0 ? <p>Nenhuma evidência registrada; isso não significa ausência de problemas.</p> : groupChecklist(room.components, (component) => component.displayGroup).map(({ group, items }) => <div className="report-checklist-group" key={group}><h5>{group}</h5><ul>{items.map((component, index) => <li key={component.label + "-" + index}><strong>{component.label}</strong><span>{component.status}</span>{component.description && <p>{component.description}</p>}{component.condition && <p>Condição observada: {component.condition}</p>}{component.reviewLabel && <p>{component.reviewLabel}</p>}{component.limitations.map((limitation, limitationIndex) => <p key={limitationIndex}>Limitação: {limitation}</p>)}{component.evidenceIds.length > 0 && <div className="report-checklist-evidence"><strong>Evidências</strong>{component.evidenceIds.map((reference) => { const item = evidenceByReference.get(reference); return <figure key={reference}>{item && item.availability === "AVAILABLE" && <EvidenceThumbnail item={item} onMediaError={onMediaError} />}<figcaption>{reference}</figcaption></figure>; })}</div>}</li>)}</ul></div>)}</section>)}
    {checklist.rooms.length === 0 && <p>Nenhuma evidência registrada; isso não significa ausência de problemas.</p>}
  </section>;
}

function ProcessingState({ status, message }: { status: string; message?: string | null }) {
  const label = ({ COMPLETED: "Concluída", FAILED: "Falha na análise", INCONCLUSIVE: "Análise inconclusiva", NOT_AUTHORIZED: "Análise não autorizada", PENDING: "Pendente" } as Record<string, string>)[status] ?? "Estado da análise não informado";
  return <section className="report-processing" aria-label="Estado da análise"><h3>{label}</h3>{message && <p>{message}</p>}</section>;
}

function HandoverReport({ entries }: { entries: NonNullable<InternalReport["handover"]>["entries"] }) {
  return <section className="report-handover"><h3>Chaves, controles e medidores</h3>{entries.length ? <ul>{entries.map((entry, index) => <li key={`${entry.kind}-${index}`}><p>{typeof entry.payload?.presentation === "string" ? entry.payload.presentation : "Registro adicional"}</p></li>)}</ul> : <p>Nenhum registro adicional.</p>}</section>;
}
function CustomerHandoverView({ entries }: { entries: NonNullable<NonNullable<CustomerReport["handover"]>["entries"]> }) { return <section className="report-handover"><h3>Chaves, controles e medidores</h3>{entries.length ? <ul>{entries.map((entry, index) => <li key={entry.kind + "-" + index}><strong>{entry.label}</strong><span>{entry.status}</span>{entry.type && <p>{entry.kind === "METER" ? entry.type : "Tipo: " + entry.type}</p>}{entry.value && <p>{entry.kind === "KEY" || entry.kind === "CONTROL" ? "Quantidade: " + entry.value : entry.kind === "NOTE" ? "Observação declarada: " + entry.value : "Leitura: " + entry.value}</p>}{entry.unit && <p>Unidade: {entry.unit}</p>}{entry.date && <p>Data: {entry.date}</p>}{entry.declarant && <p>Declarado por: {entry.declarant}</p>}</li>)}</ul> : <p>Nenhum registro adicional.</p>}</section>; }

function ComparisonReport({ comparison, evidence, onMediaError, canRevisePairing, onRevisePairing, actionsDisabled }: { comparison: NonNullable<InternalReport["comparison"]>; evidence: ReportEvidenceItem[]; onMediaError?: () => void; canRevisePairing: boolean; onRevisePairing?: (itemIdentityId: string, baselineEvidenceId: string, currentEvidenceId: string) => Promise<void>; actionsDisabled: boolean }) {
  const [filter, setFilter] = useState("");
  const [expanded, setExpanded] = useState(true);
  const evidenceById = new Map(evidence.map((item) => [item.id, item]));
  const counts = comparison.counts;
  const filtered = comparison.items.filter((item) => !filter || item.outcome === filter);
  return <section className="report-v3-comparison" aria-label="Comparação com o inventário de referência"><header><div><p>Referência · versão {comparison.baseline.versionNumber} · {comparison.baseline.purposeLabel}</p><h3>Comparação do inventário</h3><p>{comparison.statusLabel} · {formatReportDate(comparison.baseline.activatedAt)}</p></div><strong>{comparisonCountLabel(filtered.length, counts.total)}</strong></header>
    <ComparisonFilters counts={counts} selected={filter} onSelect={setFilter} />
    {comparison.status !== "COMPLETED" && <p>{comparison.statusLabel}. Os itens sem conclusão permanecem identificados.</p>}
    <div>{filtered.map((item, index) => <article className="report-v3-comparison-item" key={`${item.itemIdentityId ?? item.name}-${index}`}><header><div><p>{item.room} · {item.typeCode ? itemTypeLabel(item.typeCode) : "Componente"}</p><h4>{item.name}</h4></div><strong>{item.outcomeLabel}</strong></header>
      <p className="report-comparison-outcome">{item.outcomeLabel}</p>{item.baselineCondition && <p>Condição da referência: {reportConditionLabel(item.baselineCondition)}</p>}{item.currentCondition && <p>Condição atual: {reportConditionLabel(item.currentCondition)}</p>}{item.description && <p>{item.description}</p>}
      {item.outcome === "NO_BASIS" && <p>Não há uma vista avaliável do item na referência aprovada; por isso, não é possível comparar as condições.</p>}{item.outcome === "NOT_OBSERVED" && <p>Não houve evidência suficiente da vistoria atual para concluir a comparação.</p>}
      {item.limitations.map((limitation, limitIndex) => <p key={limitIndex}>Limitação: {limitation}</p>)}
      {item.pairs.map((pair, pairIndex) => { const baseline = evidenceById.get(pair.baselineEvidenceId); const current = evidenceById.get(pair.currentEvidenceId); return <div className="report-inventory-pair" key={`${pair.baselineEvidenceId}-${pair.currentEvidenceId}-${pairIndex}`}><EvidenceTile title="Referência" item={baseline} onMediaError={onMediaError} /><EvidenceTile title="Vistoria atual" item={current} onMediaError={onMediaError} /></div>; })}
      {item.pairingRevised && <p>Pareamento revisado pela equipe.</p>}
      {canRevisePairing && onRevisePairing && item.itemIdentityId && <PairingRevision item={item} evidence={evidence} disabled={actionsDisabled} onSubmit={(baseline, current) => onRevisePairing(item.itemIdentityId!, baseline, current)} />}
    </article>)}</div>
    {comparison.outsideBaseline.length > 0 && <details><summary>Observações fora do inventário de referência ({comparison.outsideBaseline.length})</summary><ul>{comparison.outsideBaseline.map((item, index) => <li key={index}>{item.room} · {item.name}{item.description ? ` · ${item.description}` : ""}</li>)}</ul></details>}
    {!counts.total && <p>Não há itens de referência nesta versão.</p>}
  </section>;
}
function ReportReasons({ scope, reasons }: { scope: string[]; reasons: string[] }) { return <section className="report-analysis-context">{scope.length > 0 && <><h3>Escopo da análise</h3><ul>{scope.map((item, index) => <li key={`scope-${index}`}>{item}</li>)}</ul></>}{reasons.length > 0 && <><h3>Motivos</h3><ul>{reasons.map((item, index) => <li key={`reason-${index}`}>{item}</li>)}</ul></>}</section>; }
function PairingRevision({ item, evidence, disabled, onSubmit }: { item: NonNullable<InternalReport["comparison"]>["items"][number]; evidence: ReportEvidenceItem[]; disabled: boolean; onSubmit: (baselineId: string, currentId: string) => Promise<void> }) {
  const baselineChoices = evidence.filter((entry) => entry.role === "REFERENCE");
  const currentChoices = evidence.filter((entry) => entry.role !== "REFERENCE");
  const [baseline, setBaseline] = useState(item.pairs[0]?.baselineEvidenceId ?? "");
  const [current, setCurrent] = useState(item.pairs[0]?.currentEvidenceId ?? "");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  if (!baselineChoices.length || !currentChoices.length) return <p>Não há evidências dos dois lados para revisar o pareamento.</p>;
  return <details className="report-pairing-revision"><summary>Revisar pareamento</summary><label>Imagem da referência<select disabled={disabled} value={baseline} onChange={(event) => setBaseline(event.target.value)}>{baselineChoices.map((entry) => <option key={entry.id} value={entry.id}>{entry.reference ?? entry.description ?? "Evidência"}</option>)}</select></label><label>Imagem da vistoria atual<select disabled={disabled} value={current} onChange={(event) => setCurrent(event.target.value)}>{currentChoices.map((entry) => <option key={entry.id} value={entry.id}>{entry.reference ?? entry.description ?? "Evidência"}</option>)}</select></label><button type="button" disabled={disabled || busy || !baseline || !current} onClick={() => { setBusy(true); setMessage(""); void onSubmit(baseline, current).catch(() => setMessage("Não foi possível revisar o pareamento. Confira o acesso e tente novamente.")).finally(() => setBusy(false)); }}>Salvar revisão</button>{message && <p role="status">{message}</p>}</details>;
}
function CustomerComparisonView({ comparison, evidence, onMediaError }: { comparison: NonNullable<CustomerReport["comparison"]>; evidence: ReportEvidenceItem[]; onMediaError?: () => void }) {
  const [filter, setFilter] = useState("");
  const filtered = comparison.items.filter((item) => !filter || item.outcome === filter);
  const evidenceByReference = new Map(evidence.map((item) => [item.reference, item]));
  return <section className="report-v3-comparison report-v3-comparison--customer" aria-label="Comparação com o inventário de referência"><header><div><p>Referência · versão {comparison.baselineVersionNumber} · {comparison.purposeLabel}</p><h3>Comparação do inventário</h3><p>{comparison.statusLabel} · {formatReportDate(comparison.activatedAt)}</p></div><strong>{comparisonCountLabel(filtered.length, comparison.counts.total)}</strong></header>
    <ComparisonFilters counts={comparison.counts} selected={filter} onSelect={setFilter} />
    {comparison.status !== "COMPLETED" && <p>{comparison.statusLabel}. Os itens sem conclusão permanecem identificados.</p>}
    {filtered.map((item, index) => <article className="report-v3-comparison-item" key={`${item.room}-${item.name}-${index}`}><header><div><p>{item.room}</p><h4>{item.name}</h4></div><strong>{item.outcomeLabel}</strong></header>
      {item.baselineCondition && <p>Condição da referência: {item.baselineCondition}</p>}{item.currentCondition && <p>Condição atual: {item.currentCondition}</p>}{item.outcome === "NO_BASIS" && <p>Não há uma vista avaliável do item na referência aprovada; por isso, não é possível comparar as condições.</p>}{item.outcome === "NOT_OBSERVED" && <p>Não houve evidência suficiente da vistoria atual para concluir a comparação.</p>}{item.limitations.map((limitation, limitIndex) => <p key={limitIndex}>Limitação: {limitation}</p>)}
      {item.pairs.map((pair, pairIndex) => {
        const baseline = evidenceByReference.get(pair.baselineEvidence);
        const current = evidenceByReference.get(pair.currentEvidence);
        return <div className="report-customer-pair" key={pairIndex}>
          <p><strong>{pair.baselineEvidence} ↔ {pair.currentEvidence}</strong></p>
          <div className="report-inventory-pair">
            <EvidenceTile title={`Referência · ${pair.baselineEvidence}`} item={baseline} onMediaError={onMediaError} />
            <EvidenceTile title={`Vistoria atual · ${pair.currentEvidence}`} item={current} onMediaError={onMediaError} />
          </div>
          {(!baseline || baseline.availability !== "AVAILABLE") && <p>Evidência da referência indisponível.</p>}
          {(!current || current.availability !== "AVAILABLE") && <p>Evidência atual indisponível.</p>}
        </div>;
      })}
    </article>)}
    {comparison.outsideBaseline.length > 0 && <details><summary>Observações fora do inventário de referência ({comparison.outsideBaseline.length})</summary><ul>{comparison.outsideBaseline.map((item, index) => <li key={index}>{item.room} · {item.name}</li>)}</ul></details>}
    <p>{comparison.disclaimer}</p>
  </section>;
}
function ComparisonFilters({ counts, selected, onSelect }: { counts: { total: number; unchanged: number; changed: number; improved: number; missing: number; notObserved: number; noBasis: number }; selected: string; onSelect: (value: string) => void }) {
  const options: Array<[string, string, number]> = [["", "Todos", counts.total], ...(["UNCHANGED", "CHANGED", "IMPROVED", "MISSING", "NOT_OBSERVED", "NO_BASIS"] as const).map((outcome): [string, string, number] => [outcome, comparisonOutcomeLabel(outcome), counts[outcome === "UNCHANGED" ? "unchanged" : outcome === "CHANGED" ? "changed" : outcome === "IMPROVED" ? "improved" : outcome === "MISSING" ? "missing" : outcome === "NOT_OBSERVED" ? "notObserved" : "noBasis"]])];
  return <div className="report-comparison-filters" role="group" aria-label="Filtrar resultados da comparação">{options.map(([value, label, count]) => <button type="button" key={value || "all"} aria-pressed={selected === value} onClick={() => onSelect(value)}>{label} ({count})</button>)}</div>;
}
function formatReportDate(value: string) { const date = new Date(value); return Number.isNaN(date.getTime()) ? "Data indisponível" : new Intl.DateTimeFormat("pt-BR", { dateStyle: "short" }).format(date); }

type InventoryPair = { originEvidenceId: string; currentEvidenceId: string; originSourceAvailability: string; currentSourceAvailability: string; timestampMs?: number | null };
type InventoryEntry = { environment: string; type: string; name: string; status: string; statusLabel?: string | null; conditionBefore?: string | null; conditionAfter?: string | null; reason?: string | null; pairs: InventoryPair[] };
type InventoryProjection = { referenceVersionNumber: number; purpose: string; coverage?: { expectedItems: number; assessedItems: number; notObserved: number; noBasis?: number | null; environmentsNotCaptured: string[]; environmentsAddedDuringCapture: string[] } | null; problems: InventoryEntry[]; informational: InventoryEntry[] };

function InventoryReport({ inventory, evidence, snapshotId, classification, limited, layout, onLayoutChange, collapseNotObserved = false }: { inventory: InventoryProjection; evidence: Array<ReportEvidenceItem & { mediaAvailability?: string | null }>; snapshotId: string; classification: string; limited: boolean; layout?: "cards" | "list"; onLayoutChange?: (layout: "cards" | "list") => void; collapseNotObserved?: boolean }) {
  const evidenceById = new Map(evidence.map((item) => [item.id, item]));
  const clean = classification === "NORMAL" && !limited && inventory.problems.length === 0;
  const coverage = buildCoverageSummary(inventory.coverage);
  const notObserved = collapseNotObserved ? inventory.problems.filter((item) => item.status === "NOT_OBSERVED") : [];
  const visibleProblems = collapseNotObserved ? inventory.problems.filter((item) => item.status !== "NOT_OBSERVED") : inventory.problems;
  const renderProblem = (item: InventoryEntry, index: number) => <article className="report-inventory-entry" key={`${item.environment}-${item.name}-${index}`}><div className="report-inventory-entry-copy"><p className="report-inventory-entry-location">{presentReportSection(item.environment)} · {itemTypeLabel(item.type)}</p><div className="report-inventory-entry-title"><h4>{item.name}</h4><span className="report-inventory-entry-status">{inventoryEntryStatusLabel(item)}</span></div>{(item.conditionBefore || item.conditionAfter) && <p className="report-inventory-entry-condition">{inventoryConditionLabel(item)}</p>}{item.reason && <p className="report-inventory-entry-reason">{item.reason}</p>}</div>{item.pairs.length ? <div className="report-inventory-pairs">{item.pairs.map((pair) => { const origin = evidenceById.get(pair.originEvidenceId); const current = evidenceById.get(pair.currentEvidenceId); return <div className="report-inventory-pair" key={`${pair.originEvidenceId}-${pair.currentEvidenceId}`}><div><strong>Na origem</strong>{origin ? origin.evidenceKind === "VIDEO_FRAME" ? <VideoEvidencePlayer item={{ ...origin, snapshotId, sourceAvailability: pair.originSourceAvailability }} /> : <EvidenceThumbnail item={{ ...origin, snapshotId, sourceAvailability: pair.originSourceAvailability }} /> : <span>{pair.originSourceAvailability === "EXPIRED" ? "Quadro preservado" : "Evidência de origem indisponível"}</span>}<small>{reportAvailabilityLabel(pair.originSourceAvailability)}</small>{origin?.timestampMs != null && <small>Momento {formatVideoTime(origin.timestampMs)}</small>}</div><div><strong>Na vistoria atual</strong>{current ? current.evidenceKind === "VIDEO_FRAME" ? <VideoEvidencePlayer item={{ ...current, snapshotId, timestampMs: pair.timestampMs ?? current.timestampMs, sourceAvailability: pair.currentSourceAvailability }} /> : <EvidenceThumbnail item={{ ...current, snapshotId, sourceAvailability: pair.currentSourceAvailability }} /> : <span>Evidência atual indisponível</span>}<small>{pair.timestampMs != null ? `Momento ${formatVideoTime(pair.timestampMs)}` : reportAvailabilityLabel(pair.currentSourceAvailability)}</small></div></div>; })}</div> : <p className="report-inventory-entry-no-image">Sem imagem correspondente</p>}</article>;
  return <section className={`report-inventory${onLayoutChange ? ` report-inventory--${layout}` : ""}`} aria-label="Comparação do inventário">
    <header><div><p className="report-kicker">Inventário de referência · versão {inventory.referenceVersionNumber}</p><h3>{clean ? "Sem alertas no inventário" : inventory.problems.length ? "Problemas encontrados" : "Resumo do inventário"}</h3><p>Propósito: {purposeLabel(inventory.purpose)}</p></div>{coverage && <strong>{coverage.label}</strong>}</header>
    {onLayoutChange && inventory.problems.length > 0 && <div className="report-inventory-layout" role="group" aria-label="Visualização dos itens"><span>Visualização dos itens</span><div><button type="button" aria-pressed={layout === "cards"} onClick={() => onLayoutChange("cards")}>Cartões</button><button type="button" aria-pressed={layout === "list"} onClick={() => onLayoutChange("list")}>Lista</button></div></div>}
    {clean ? <p>Todos os itens esperados foram verificados sem alterações relevantes.</p> : <>
      {inventory.coverage?.notObserved && notObserved.length === 0 ? <p className="report-inventory-coverage">{inventory.coverage.notObserved} {inventory.coverage.notObserved === 1 ? "item não observado" : "itens não observados"}</p> : null}
      {noBasisLabel(inventory.coverage?.noBasis) ? <p className="report-inventory-coverage">{noBasisLabel(inventory.coverage?.noBasis)}</p> : null}
      {inventory.coverage?.environmentsNotCaptured.length ? <p>Ambientes não capturados: {inventory.coverage.environmentsNotCaptured.map(presentReportSection).join(", ")}</p> : null}
      {visibleProblems.length > 0 && <div className="report-inventory-items">{visibleProblems.map(renderProblem)}</div>}
      {notObserved.length > 0 && <details className="report-inventory-disclosure"><summary>Itens não observados ({notObserved.length})</summary><div className="report-inventory-items">{notObserved.map(renderProblem)}</div></details>}
      {inventory.informational.length > 0 && <details className="report-inventory-disclosure"><summary>Melhorias e itens informativos ({inventory.informational.length})</summary>{inventory.informational.map((item, index) => <p key={`${item.environment}-${item.name}-${index}`}>{presentReportSection(item.environment)} · {item.name} · {inventoryEntryStatusLabel(item)}</p>)}</details>}
    </>}
    {inventory.coverage?.environmentsAddedDuringCapture.length ? <p className="report-inventory-added">Ambientes adicionados nesta vistoria: {inventory.coverage.environmentsAddedDuringCapture.map(presentReportSection).join(", ")}</p> : null}
  </section>;
}

function inventoryConditionLabel(item: InventoryEntry) {
  if (item.conditionBefore && item.conditionAfter) return `Antes: ${conditionLabel(item.conditionBefore)} · Agora: ${conditionLabel(item.conditionAfter)}`;
  if (item.conditionBefore) return `Condição anterior: ${conditionLabel(item.conditionBefore)}`;
  return `Condição atual: ${conditionLabel(item.conditionAfter)}`;
}
function reportAvailabilityLabel(value: string) { return ({ AVAILABLE: "Original disponível", RETAINED: "Original disponível", ACTIVE_BASELINE: "Disponível enquanto o inventário estiver ativo", CONTESTATION_HOLD: "Mantido enquanto a contestação estiver aberta", LEGAL_HOLD: "Mantido por retenção legal", EXPIRED: "Original não disponível; quadro preservado", MISSING: "Evidência indisponível" } as Record<string, string>)[value] ?? "Evidência indisponível"; }

function CustomerReportHeader({ classificationLabel, advisory, context, version, historical, showVersionState }: { classificationLabel: string; advisory: string; context: CustomerReport["context"]; version: number; historical: boolean; showVersionState: boolean }) {
  return <header className="report-header">
    <div><p className="report-kicker">Laudo de vistoria · versão {version}{showVersionState && historical ? " · versão anterior" : showVersionState ? " · versão vigente" : ""}</p><h2>{context.asset.name}</h2><p>{context.asset.address}</p><p>{context.asset.externalKey} · Responsável: {context.participant.name}</p></div>
    <div className="report-result">{classificationLabel && <strong>{classificationLabel}</strong>}<span>{context.template.name} · v{context.template.version}</span>{context.inspection.stageLabel && <span>{context.inspection.stageLabel}</span>}</div>
    <p className="report-advisory">{advisory}</p>
  </header>;
}

function ReportCounts({ rooms, findingCount, compact = false }: { rooms: ReportRoom[]; findingCount: number; compact?: boolean }) {
  const items = rooms.flatMap((room) => room.items);
  const recapture = items.filter((item) => item.kind === "recapture").length;
  const pending = items.filter((item) => item.kind === "pending").length;
  const clear = items.filter((item) => item.kind === "clear").length;
  return <span className={compact ? "report-room-counts" : "report-overall"}>
    {findingCount > 0 && <span className="report-pill report-pill--warning">{findingCount} {findingCount === 1 ? "achado" : "achados"}{!compact && " para avaliar"}</span>}
    {recapture > 0 && <span className="report-pill report-pill--warning">{recapture} {compact ? (recapture === 1 ? "recaptura" : "recapturas") : (recapture === 1 ? "item para recaptura" : "itens para recaptura")}</span>}
    {pending > 0 && <span className="report-pill">{pending} {pending === 1 ? "análise pendente" : "análises pendentes"}</span>}
    {clear > 0 && <span className="report-pill">{clear} {clear === 1 ? "item sem achados" : "itens sem achados"}</span>}
    {!findingCount && !recapture && !pending && !clear && <span className="report-pill">Sem achados registrados</span>}
  </span>;
}

function ReportSections({ rooms, onMediaError }: { rooms: ReportRoom[]; onMediaError?: () => void }) {
  if (!rooms.length) return <p className="report-empty">Nenhuma evidência associada a este laudo.</p>;
  return <div className="report-room-list" aria-label="Laudo por ambiente">
    {rooms.map((room) => <RoomSection key={room.name} room={room} onMediaError={onMediaError} />)}
  </div>;
}

type ReportEvidenceItem = { id: string; snapshotId?: string; requirementKey: string; role: string; description?: string | null; capturedAt?: string | null; flags: string[]; availability: string; sourceAvailability?: string | null; url?: string | null; evidenceKind?: string | null; frameId?: string | null; timestampMs?: number | null; ptsMicros?: string | null; sourceMediaId?: string | null; component?: string | null; label?: string | null; reference?: string | null };

function RoomSection({ room, onMediaError }: { room: ReportRoom; onMediaError?: () => void }) {
  const [expanded, setExpanded] = useState(false);
  const [hasExpanded, setHasExpanded] = useState(false);
  const contentId = useId();
  const findingTitles = [...new Set(room.findings.map((finding) => finding.title))];
  return <section className="report-room">
    <button type="button" className="report-room-toggle" aria-expanded={expanded} aria-controls={contentId} onClick={() => { setExpanded((open) => !open); setHasExpanded(true); }}>
      <Icon className={expanded ? "report-room-chevron report-room-chevron--open" : "report-room-chevron"} name="chevron-down" size={18} />
      <span className="report-room-heading"><strong>{room.name}</strong>{findingTitles.length > 0 && <span className="report-room-preview">{findingTitles.join(" · ")}</span>}</span>
      <ReportCounts rooms={[room]} findingCount={room.findings.length} compact />
    </button>
    <div id={contentId} className="report-room-content" hidden={!expanded}>
      {hasExpanded && room.items.map((item, index) => <div key={item.key}>
        {item.kind !== "finding" && (index === 0 || room.items[index - 1].kind !== item.kind) && <p className="report-group-label">{item.kind === "recapture" ? "Para recaptura" : item.kind === "pending" ? "Análise pendente" : "Sem achados registrados"}</p>}
        {item.kind === "finding" ? <FindingRequirement item={item} onMediaError={onMediaError} /> : <CompactRequirement item={item} onMediaError={onMediaError} />}
      </div>)}
    </div>
  </section>;
}

function ItemEvidence({ item, onMediaError }: { item: RoomItem; onMediaError?: () => void }) {
  return <div className="report-item-evidence">{item.entries.map((entry) => <EvidencePairs key={entry.requirement.key} reference={entry.evidence.filter((evidence) => evidence.role === "REFERENCE")} current={entry.evidence.filter((evidence) => evidence.role !== "REFERENCE")} onMediaError={onMediaError} />)}</div>;
}

function FindingRequirement({ item, onMediaError }: { item: RoomItem; onMediaError?: () => void }) {
  return <article className="report-room-requirement report-room-requirement--finding">
    <header className="report-finding-header"><span>{item.title}</span><span className="report-pill report-pill--warning">Requer atenção</span></header>
    <div className="report-finding-copy">{item.findings.map((finding) => <div key={finding.id ?? finding.title}><h3>{finding.title}</h3><p>{finding.description}</p></div>)}</div>
    <ItemEvidence item={item} onMediaError={onMediaError} />
    {item.findings.filter((finding) => finding.recommendedAction).map((finding) => <div className="report-finding-action" key={finding.id ?? finding.title}><span>Ação recomendada</span><p>{finding.recommendedAction}</p></div>)}
  </article>;
}

function CompactRequirement({ item, onMediaError }: { item: RoomItem; onMediaError?: () => void }) {
  const [expanded, setExpanded] = useState(false);
  const [hasExpanded, setHasExpanded] = useState(false);
  const contentId = useId();
  const requirement = item.entries[0]?.requirement;
  const hasReference = item.entries.some((entry) => entry.evidence.some((evidence) => evidence.role === "REFERENCE"));
  const state = requirement?.analysisStatus === "COMPLETED" && requirement.noRelevantChange === true ? "Comparação concluída sem alteração relevante identificada." : requirement?.analysisMode === "CURRENT_ONLY" ? "Sem comparação concluída" : item.kind === "recapture" ? "Comparação pendente" : presentAnalysisStatus(requirement?.analysisStatus);
  return <article className="report-room-requirement report-room-requirement--compact">
    <header className="report-compact-heading"><h3>{item.title}</h3><span className={`report-pill${item.kind === "recapture" ? " report-pill--warning" : ""}`}>{item.state}</span></header>
    <div className="report-compact-copy">
      {item.instructions[0] && <p>{item.instructions[0]}</p>}
      {item.instructions.length > 1 && <p className="report-compact-more">Mais {item.instructions.length - 1} {item.instructions.length === 2 ? "vista" : "vistas"} ao ver as evidências.</p>}
    </div>
    <p className="report-compact-state">{state}</p>
    {item.entries.some((entry) => entry.evidence.length > 0) && <>
      <button type="button" className="report-evidence-toggle" aria-expanded={expanded} aria-controls={contentId} aria-label={`${expanded ? "Ocultar" : "Ver"} evidências de ${item.title}`} onClick={() => { setExpanded((open) => !open); setHasExpanded(true); }}><Icon name="chevron-down" size={16} />{expanded ? "Ocultar evidências" : "Ver evidências"}</button>
      <div id={contentId} hidden={!expanded} className="report-evidence-expanded">{hasExpanded && <>
        {item.instructions.length > 1 && <div className="report-recapture-views"><strong>Orientações por vista</strong><ul>{item.instructions.map((instruction) => <li key={instruction}>{instruction}</li>)}</ul></div>}
        {requirement?.analysisMode === "CURRENT_ONLY" && hasReference && <p className="report-compact-state">Origem disponível; comparação não concluída.</p>}
        <ItemEvidence item={item} onMediaError={onMediaError} />
      </>}</div>
    </>}
  </article>;
}

function EvidencePairs({ reference, current, onMediaError }: { reference: ReportEvidenceItem[]; current: ReportEvidenceItem[]; onMediaError?: () => void }) {
  const count = Math.max(reference.length, current.length);
  if (!count) return <p className="report-empty">Nenhuma evidência associada a este requisito.</p>;
  return <div className="report-pairs" aria-label="Comparação de evidências">
    {Array.from({ length: count }, (_, index) => <EvidencePair key={`${reference[index]?.id ?? "sem-origem"}:${current[index]?.id ?? "sem-atual"}`} reference={reference[index]} current={current[index]} onMediaError={onMediaError} />)}
  </div>;
}

function EvidencePair({ reference, current, onMediaError }: { reference?: ReportEvidenceItem; current?: ReportEvidenceItem; onMediaError?: () => void }) {
  const currentIsVideo = Boolean(current && isVideoEvidence(current));
  const referenceIsVideo = Boolean(reference && isVideoEvidence(reference));
  const playable = currentIsVideo ? current : referenceIsVideo ? reference : undefined;
  const [active, setActive] = useState<ReportEvidenceItem | null>(null);
  const playButtonRef = useRef<HTMLButtonElement | null>(null);
  const initial = currentIsVideo ? current : reference ?? current;
  const openVideo = (item: ReportEvidenceItem, button: HTMLButtonElement) => {
    playButtonRef.current = button;
    setActive(item);
  };
  const closeVideo = () => {
    setActive(null);
    window.setTimeout(() => playButtonRef.current?.focus(), 0);
  };
  return <div className="report-evidence-pair">
    <EvidenceTile title="Origem" item={reference} onPlay={playable?.id === reference?.id ? (button) => initial && openVideo(initial, button) : undefined} onMediaError={onMediaError} />
    <EvidenceTile title="Vistoria atual" item={current} onPlay={playable?.id === current?.id ? (button) => initial && openVideo(initial, button) : undefined} onMediaError={onMediaError} />
    {playable && <PairVideoModal active={active} currentVideo={currentIsVideo ? current : undefined} referenceVideo={referenceIsVideo ? reference : undefined} onSelect={setActive} onClose={closeVideo} />}
  </div>;
}

function EvidenceTile({ title, item, onPlay, onMediaError }: { title: string; item?: ReportEvidenceItem; onPlay?: (button: HTMLButtonElement) => void; onMediaError?: () => void }) {
  if (!item) return <div className="report-evidence-tile"><h4>{title}</h4><div className="report-image-missing">Sem imagem</div></div>;
  return <div className="report-evidence-tile"><h4>{title}</h4><figure>
    <div className="report-evidence-photo">
      <EvidenceThumbnail item={item} onMediaError={onMediaError} />
      {onPlay && <PairVideoLauncher item={item} onPlay={onPlay} />}
    </div>
    <figcaption>{item.description && !/^Quadro do vídeo em \d+ ms$/i.test(item.description) ? item.description : null}{item.sourceAvailability === "EXPIRED" && isVideoEvidence(item) && <small>Vídeo original expirado; frame preservado.</small>}</figcaption>
  </figure></div>;
}

function PairVideoLauncher({ item, onPlay }: { item: ReportEvidenceItem; onPlay: (button: HTMLButtonElement) => void }) {
  const context = item.label ?? item.component ?? "da vistoria";
  return <button className="report-pair-play" type="button" aria-label={`Reproduzir vídeo de ${context} a partir de ${formatVideoTime(item.timestampMs) ?? "evidência"}`} onClick={(event) => onPlay(event.currentTarget)}><span aria-hidden="true">▶</span></button>;
}

function PairVideoModal({ active, currentVideo, referenceVideo, onSelect, onClose }: { active: ReportEvidenceItem | null; currentVideo?: ReportEvidenceItem; referenceVideo?: ReportEvidenceItem; onSelect: (item: ReportEvidenceItem) => void; onClose: () => void }) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const title = active ? `${active.label ?? active.component ?? "Vídeo da vistoria"} · ${active.role === "REFERENCE" ? "Origem" : "Atual"}` : "Reproduzir vídeo";
  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    if (active && !dialog.open) {
      if (typeof dialog.showModal === "function") dialog.showModal();
      else dialog.setAttribute("open", "");
    } else if (!active && dialog.open) {
      if (typeof dialog.close === "function") dialog.close();
      else dialog.removeAttribute("open");
    }
  }, [active]);
  const closeDialog = () => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    if (typeof dialog.close === "function") dialog.close();
    else { dialog.removeAttribute("open"); onClose(); }
  };
  return <>
    <dialog ref={dialogRef} className="report-video-dialog" aria-labelledby={titleId} onClose={onClose} onCancel={(event) => { event.preventDefault(); closeDialog(); }} onKeyDown={(event) => { if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); closeDialog(); } }} onClick={(event) => { if (event.target === event.currentTarget) closeDialog(); }}>
      <header><h2 id={titleId}>{title}</h2><button type="button" aria-label="Fechar vídeo" onClick={closeDialog}><Icon name="close" size={20} /></button></header>
      {active && <VideoModalContent key={active.id} item={active} currentVideo={currentVideo} referenceVideo={referenceVideo} onSelect={onSelect} />}
    </dialog>
  </>;
}

function VideoModalContent({ item, currentVideo, referenceVideo, onSelect }: { item: ReportEvidenceItem; currentVideo?: ReportEvidenceItem; referenceVideo?: ReportEvidenceItem; onSelect: (item: ReportEvidenceItem) => void }) {
  const [state, setState] = useState<"preparing" | "ready" | "unavailable" | "unsupported">("preparing");
  const [playBlocked, setPlayBlocked] = useState(false);
  const [retry, setRetry] = useState(0);
  const videoRef = useRef<HTMLVideoElement>(null);
  const timestamp = item.timestampMs;
  const endpoint = videoContentUrl(item);
  const expired = item.sourceAvailability === "EXPIRED" || item.sourceAvailability === "MISSING";

  useEffect(() => {
    let cancelled = false;
    if (expired || !endpoint || timestamp === null || timestamp === undefined || timestamp < 0) { setState("unavailable"); return; }
    setState("preparing");
    void prepareVideoPlaybackSession().then(() => { if (!cancelled) setState("ready"); }).catch(() => { if (!cancelled) setState("unavailable"); });
    return () => { cancelled = true; };
  }, [endpoint, expired, item.id, retry, timestamp]);

  const startAtEvidence = () => {
    const video = videoRef.current;
    if (!video || timestamp === null || timestamp === undefined) return;
    if (!Number.isFinite(video.duration) || timestamp / 1000 >= video.duration) { setState("unavailable"); return; }
    video.currentTime = timestamp / 1000;
    void video.play().catch(() => setPlayBlocked(true));
  };

  return <div className="report-video-modal-content">
    {(currentVideo && referenceVideo) && <div className="report-video-source-switch" aria-label="Selecionar vídeo">
      <button type="button" aria-pressed={item.role === "REFERENCE"} onClick={() => onSelect(referenceVideo)}>Origem</button>
      <button type="button" aria-pressed={item.role !== "REFERENCE"} onClick={() => onSelect(currentVideo)}>Atual</button>
    </div>}
    {state === "ready" && <video ref={videoRef} src={endpoint} preload="metadata" controls playsInline aria-label={`Vídeo ${item.role === "REFERENCE" ? "da origem" : "da vistoria atual"}`} onLoadedMetadata={startAtEvidence} onPlay={() => setPlayBlocked(false)} onError={() => setState("unsupported")} />}
    {state === "preparing" && <p role="status">Preparando reprodução…</p>}
    {state === "unavailable" && <p role="status">Vídeo indisponível neste momento; a imagem do frame continua no laudo.</p>}
    {state === "unsupported" && <p role="status">Este navegador não conseguiu reproduzir o vídeo; a imagem do frame continua no laudo.</p>}
    {playBlocked && <p role="status">Use os controles do vídeo para iniciar a reprodução.</p>}
    {(state === "unavailable" && !expired || state === "unsupported") && <button type="button" onClick={() => { setState("preparing"); setPlayBlocked(false); setRetry((value) => value + 1); }}>Tentar novamente</button>}
    <p className="report-video-time-note">Ponto da evidência · {formatVideoTime(timestamp) ?? "indisponível"}</p>
  </div>;
}

function ReportEvidence({ requirements, evidence, findings, hasReferenceInReport, onMediaError }: { requirements: Array<{ key: string; section: string; label: string; instructions?: string | null; noRelevantChange?: boolean | null; analysisMode: string; analysisStatus: string }>; evidence: ReportEvidenceItem[]; findings: Array<{ category?: string; title: string; description: string; severity: string; recommendedAction: string; evidenceIds: string[] }>; hasReferenceInReport: boolean; onMediaError?: () => void }) {
  const unmatched = unmatchedReportEvidence(requirements, evidence);
  const unmatchedReference = unmatched.filter((item) => item.role === "REFERENCE");
  const unmatchedCurrent = unmatched.filter((item) => item.role !== "REFERENCE");
  const findingIds = new Set(findings.flatMap((finding) => finding.evidenceIds));
  const findingKeys = new Set(evidence.filter((item) => findingIds.has(item.id)).map((item) => item.requirementKey));
  const orderedRequirements = [...requirements].sort((a, b) => Number(findingKeys.has(b.key)) - Number(findingKeys.has(a.key)));
  return <section className="report-requirements" aria-label="Evidências do laudo">
    {orderedRequirements.map((requirement) => {
      const items = evidence.filter((item) => item.requirementKey === requirement.key);
      const related = findings.filter((finding) => finding.evidenceIds.some((id) => items.some((item) => item.id === id)));
      const reference = items.filter((item) => item.role === "REFERENCE");
      const current = items.filter((item) => item.role !== "REFERENCE");
      const comparison = <div className="report-comparison">{reference.length > 0 && <EvidenceColumn title="Referência" items={reference} onMediaError={onMediaError} />}<EvidenceColumn title={reference.length > 0 ? "Vistoria atual" : "Fotos da vistoria"} items={current} onMediaError={onMediaError} /></div>;
      const baselineNote = requirement.analysisMode === "CURRENT_ONLY" && <p className="report-no-baseline">{baselineMessage(hasReferenceInReport)}</p>;
      return <section className="report-requirement" key={requirement.key}>
      <header className="report-requirement-heading"><div><span>{presentReportSection(requirement.section)}</span><h3>{presentReportLabel(requirement.label)}</h3>{requirement.instructions && <p>{presentReportLabel(requirement.instructions)}</p>}</div><small>{presentAnalysisStatus(requirement.analysisStatus)}{related.length === 0 && requirement.analysisMode === "COMPARE_ORIGIN_CURRENT" && requirement.noRelevantChange !== null && <> · {presentNoRelevantChange(requirement.noRelevantChange)}</>}</small></header>
        <div className={`report-requirement-body${related.length ? "" : " report-requirement-body--without-findings"}`}>
          {related.length > 0 && baselineNote}
          {related.length > 0 && <section className="report-findings" aria-label={`Constatações de ${presentReportLabel(requirement.label)}`}>{related.map((finding) => <article key={`${finding.title}-${finding.description}`}><span>{presentFindingCategory(finding.category)} · {presentFindingSeverity(finding.severity)}</span><h4>{finding.title}</h4><p>{finding.description}</p><p><strong>Ação recomendada</strong> · {finding.recommendedAction}</p></article>)}</section>}
          {related.length > 0 ? comparison : <details className="report-evidence-details"><summary aria-label={`Ver evidências de ${requirement.label}`}>Ver evidências</summary>{baselineNote}{comparison}{requirement.analysisStatus === "COMPLETED" && <p className="report-no-findings">Nenhuma constatação foi registrada; isso não confirma a ausência de problemas.</p>}</details>}
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
  return <section className="report-evidence-column">
    <h4>{videoEvidence ? (title === "Referência" ? "ORIGEM" : "ATUAL") : title}</h4>
    {items.length ? <div className="report-image-grid">{items.map((item) => <figure key={`${item.id}:${item.url ?? ""}`}>
      {isVideoEvidence(item)
        ? <VideoEvidencePlayer item={item} onMediaError={onMediaError} />
        : <EvidenceThumbnail item={item} onMediaError={onMediaError} />}
      <figcaption>
        {isVideoEvidence(item) ? <details className="report-media-details"><summary>Detalhes da evidência</summary>
          <p>{item.description ?? item.label ?? "Sem descrição"}</p>
          <small>{[item.component, formatVideoTime(item.timestampMs), item.frameId ? `Frame ${shortFrameId(item.frameId)}` : undefined].filter(Boolean).join(" · ")}</small>
          {item.ptsMicros && <small>PTS {formatVideoTime(Number(item.ptsMicros) / 1000)}</small>}
        </details> : item.description ?? item.label ?? "Sem descrição"}
        {item.capturedAt && <small>{new Date(item.capturedAt).toLocaleString("pt-BR")}</small>}
        {item.flags.length > 0 && <small>{item.flags.join(" · ")}</small>}
      </figcaption>
    </figure>)}</div> : <p className="report-empty">Nenhuma evidência associada a este requisito.</p>}
  </section>;
}

function EvidenceThumbnail({ item, onMediaError }: { item: ReportEvidenceItem; onMediaError?: () => void }) {
  return item.availability === "AVAILABLE" && item.url
    ? <EvidenceImage url={item.url} alt={item.label ?? item.component ?? item.description ?? "Evidência da vistoria"} onMediaError={onMediaError} />
    : <div className="report-image-missing">Imagem indisponível</div>;
}

/** Same-origin BFF URL; the report snapshot scopes which videos the API will serve. */
export function videoContentUrl(item: ReportEvidenceItem): string {
  return item.sourceMediaId && item.snapshotId ? `/api/media/videos/${encodeURIComponent(item.sourceMediaId)}/content?${new URLSearchParams({ snapshotId: item.snapshotId })}` : "";
}

function baselineMessage(hasReferenceInReport: boolean): string {
  return hasReferenceInReport ? "A origem existe neste laudo, mas não há comparação concluída para este requisito." : "Esta análise mostra apenas a condição observada agora; não há referência histórica para comparar.";
}

function isVideoEvidence(item: ReportEvidenceItem): boolean { return item.evidenceKind === "VIDEO_FRAME" && Boolean(item.sourceMediaId); }
function formatVideoTime(timestampMs?: number | null): string | undefined {
  if (timestampMs === null || timestampMs === undefined || !Number.isFinite(timestampMs) || timestampMs < 0) return undefined;
  const seconds = Math.floor(timestampMs / 1000);
  return `${String(Math.floor(seconds / 60)).padStart(2, "0")}:${String(seconds % 60).padStart(2, "0")}`;
}
function shortFrameId(frameId: string): string { return frameId.slice(0, 8); }

export function VideoEvidencePlayer({ item, onMediaError }: { item: ReportEvidenceItem; onMediaError?: () => void }) {
  const [state, setState] = useState<"idle" | "preparing" | "ready" | "unavailable" | "unsupported">("idle");
  const [sessionReady, setSessionReady] = useState(false);
  const [seekUnavailable, setSeekUnavailable] = useState(false);
  const [playBlocked, setPlayBlocked] = useState(false);
  const videoRef = useRef<HTMLVideoElement>(null);
  const expired = item.sourceAvailability === "EXPIRED" || item.sourceAvailability === "MISSING";
  const timestampMs = item.timestampMs;
  const validTimestamp = timestampMs !== null && timestampMs !== undefined && Number.isFinite(timestampMs) && timestampMs >= 0;
  const endpoint = videoContentUrl(item);
  const showVideo = sessionReady && !expired && !seekUnavailable && state !== "unsupported";

  const activate = async () => {
    if (state === "preparing") return;
    if (expired) { setState("unavailable"); return; }
    if (!validTimestamp || !endpoint) { setSeekUnavailable(true); setState("unavailable"); return; }
    setState("preparing");
    setPlayBlocked(false);
    try {
      await prepareVideoPlaybackSession();
      setSessionReady(true);
      setState("ready");
    } catch {
      // A playback failure must not reload the report and erase this feedback.
      setState("unavailable");
    }
  };

  const seekToEvidence = () => {
    const video = videoRef.current;
    if (!video || !validTimestamp || timestampMs === null || timestampMs === undefined) return false;
    if (!seekVideoToEvidence(video, timestampMs)) { setSeekUnavailable(true); setState("unavailable"); return false; }
    return true;
  };

  const startPlayback = () => {
    if (!seekToEvidence()) return;
    void videoRef.current?.play().catch(() => setPlayBlocked(true));
  };

  return <div className="report-video-player" aria-busy={state === "preparing"}>
    {showVideo ? <div className="report-video-controls">
      <video ref={videoRef} src={endpoint} preload="metadata" controls playsInline aria-label={`Vídeo da evidência ${item.label ?? item.component ?? "da vistoria"}`} onLoadedMetadata={startPlayback} onPlay={() => setPlayBlocked(false)} onError={() => { setSessionReady(false); setState("unsupported"); }} />
      <button type="button" onClick={seekToEvidence}>Ir para {formatVideoTime(timestampMs)}</button>
      {playBlocked && <p role="status">Use o controle de reprodução do vídeo para continuar.</p>}
    </div> : expired ? <EvidenceThumbnail item={item} onMediaError={onMediaError} /> : <button type="button" className="report-video-activate" onClick={() => void activate()} disabled={state === "preparing"} aria-label={`Reproduzir vídeo no ponto ${formatVideoTime(timestampMs) ?? "indisponível"}`}>
      <EvidenceThumbnail item={item} onMediaError={onMediaError} />
      <span className="report-video-play-icon" aria-hidden="true">▶</span>
      <span className="report-video-time" aria-hidden="true">{formatVideoTime(timestampMs) ?? "Vídeo"}</span>
    </button>}
    {expired && <p role="status">Vídeo original expirado; frame preservado.</p>}
    {seekUnavailable && <p role="status">Evidência de vídeo indisponível neste timestamp.</p>}
    {state === "preparing" && <p role="status">Preparando reprodução…</p>}
    {state === "unavailable" && !expired && !seekUnavailable && <p role="status">Não foi possível carregar o vídeo. Clique na imagem para tentar novamente.</p>}
    {state === "unsupported" && <p role="status">Não foi possível reproduzir o vídeo. As imagens e constatações continuam disponíveis. Clique na imagem para tentar novamente.</p>}
  </div>;
}

export function seekVideoToEvidence(video: Pick<HTMLVideoElement, "duration" | "currentTime">, timestampMs: number) {
  if (!Number.isFinite(timestampMs) || timestampMs < 0 || !Number.isFinite(video.duration) || timestampMs / 1000 >= video.duration) return false;
  video.currentTime = timestampMs / 1000;
  return true;
}

export function EvidenceImage({ url, alt, onMediaError }: { url: string; alt: string; onMediaError?: () => void }) {
  const [blobURL, setBlobURL] = useState<string>();
  const [unavailable, setUnavailable] = useState(false);
  const onError = useRef(onMediaError);
  useEffect(() => { onError.current = onMediaError; }, [onMediaError]);
  const authenticatedFrame = url.startsWith("/media/video-evidence/");
  useEffect(() => {
    if (!authenticatedFrame) return;
    const controller = new AbortController();
    let createdURL: string | undefined;
    setBlobURL(undefined);
    setUnavailable(false);
    const load = async () => {
      const token = await accessTokenForRequest();
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
    void load().catch(() => { if (!controller.signal.aborted) { setUnavailable(true); onError.current?.(); } });
    return () => { controller.abort(); if (createdURL) URL.revokeObjectURL(createdURL); };
  }, [authenticatedFrame, url]);
  if (unavailable) return <span className="report-image-missing">Evidência indisponível</span>;
  if (authenticatedFrame && !blobURL) return <div className="report-image-missing">Carregando frame…</div>;
  return <img src={authenticatedFrame ? blobURL : url} loading="lazy" alt={alt} onError={() => { setUnavailable(true); onMediaError?.(); }} />;
}
