import Link from "next/link";
import { useCallback, useEffect, useState } from "react";
import { getMembershipId } from "@/auth/session";
import type { InspectionsQuery } from "@/graphql/generated";

export type InspectionView = "lista" | "quadro" | "agenda";
export type InspectionRecord = InspectionsQuery["inspections"]["nodes"][number];
export type InspectionActionHandlers = {
  onCancel: (inspection: InspectionRecord) => void;
  onInvalidate: (inspection: InspectionRecord) => void;
  onRecapture: (inspection: InspectionRecord) => void;
};

export const inspectionViewOptions: Array<{ value: InspectionView; label: string }> = [
  { value: "lista", label: "Lista" },
  { value: "quadro", label: "Quadro" },
  { value: "agenda", label: "Agenda" },
];

const inspectionViewValues = new Set<InspectionView>(inspectionViewOptions.map(({ value }) => value));
const storagePrefix = "dashboard.inspections.view.";

const inspectionColumns = [
  { key: "planejamento", label: "Planejamento", statuses: ["PLANNED", "INVITED"] },
  { key: "execucao", label: "Em execução", statuses: ["IN_PROGRESS", "SUBMITTED", "ANALYZING", "RECAPTURE_PENDING"] },
  { key: "concluidas", label: "Concluídas", statuses: ["COMPLETED"] },
  { key: "encerradas", label: "Encerradas", statuses: ["CANCELED", "INVALIDATED"] },
] as const;

const inspectionStatusLabels: Record<string, string> = {
  PLANNED: "Planejada",
  INVITED: "Convidada",
  IN_PROGRESS: "Em andamento",
  SUBMITTED: "Enviada",
  ANALYZING: "Em análise",
  RECAPTURE_PENDING: "Recaptura pendente",
  COMPLETED: "Concluída",
  CANCELED: "Cancelada",
  INVALIDATED: "Invalidada",
};
export type InspectionColumnKey = (typeof inspectionColumns)[number]["key"];

export function inspectionViewStorageKey(membershipId?: string): string {
  return `${storagePrefix}${membershipId || "default"}`;
}

export function isInspectionView(value: string | null | undefined): value is InspectionView {
  return value !== null && value !== undefined && inspectionViewValues.has(value as InspectionView);
}

export function readInspectionView(membershipId?: string): InspectionView {
  if (typeof window === "undefined") return "lista";
  try {
    const stored = window.localStorage.getItem(inspectionViewStorageKey(membershipId));
    return isInspectionView(stored) ? stored : "lista";
  } catch {
    return "lista";
  }
}

export function persistInspectionView(membershipId: string | undefined, view: InspectionView): void {
  if (typeof window === "undefined") return;
  try {
    window.localStorage.setItem(inspectionViewStorageKey(membershipId), view);
  } catch {
    // A blocked or unavailable localStorage should not prevent the dashboard from working.
  }
}

export function getInspectionColumn(status: string): InspectionColumnKey {
  return inspectionColumns.find((column) => column.statuses.some((candidate) => candidate === status))?.key ?? "planejamento";
}

export function groupInspectionsByStatus(inspections: InspectionRecord[]): Record<InspectionColumnKey, InspectionRecord[]> {
  const groups: Record<InspectionColumnKey, InspectionRecord[]> = { planejamento: [], execucao: [], concluidas: [], encerradas: [] };
  for (const inspection of inspections) groups[getInspectionColumn(inspection.status)].push(inspection);
  return groups;
}

export function sortInspectionsByDueAt(inspections: InspectionRecord[]): InspectionRecord[] {
  return inspections
    .map((inspection, index) => ({ inspection, index, timestamp: parseDate(inspection.dueAt)?.getTime() ?? Number.POSITIVE_INFINITY }))
    .sort((left, right) => left.timestamp - right.timestamp || left.index - right.index)
    .map(({ inspection }) => inspection);
}

export function filterInspections(inspections: InspectionRecord[], query: string, column: InspectionColumnKey | "todas"): InspectionRecord[] {
  const normalizedQuery = query.trim().toLocaleLowerCase("pt-BR");
  return inspections.filter((inspection) => {
    const matchesColumn = column === "todas" || getInspectionColumn(inspection.status) === column;
    const searchable = [inspection.id, inspection.assetId, inspection.participantId, inspection.source, inspection.status].join(" ").toLocaleLowerCase("pt-BR");
    return matchesColumn && (!normalizedQuery || searchable.includes(normalizedQuery));
  });
}

export function formatInspectionStatus(status: string): string {
  return inspectionStatusLabels[status] ?? "Status desconhecido";
}

export function formatInspectionDate(value: string | null | undefined): string {
  if (!value) return "Data não informada";
  const date = parseDate(value);
  if (!date) return "Data inválida";
  return new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" }).format(date);
}

export function useInspectionView(): [InspectionView, (view: InspectionView) => void] {
  const membershipId = getMembershipId();
  const [view, setView] = useState<InspectionView>("lista");

  useEffect(() => {
    setView(readInspectionView(membershipId));
  }, [membershipId]);

  const changeView = useCallback((nextView: InspectionView) => {
    setView(nextView);
    persistInspectionView(membershipId, nextView);
  }, [membershipId]);

  return [view, changeView];
}

export function InspectionViewSelector({ view, onChange }: { view: InspectionView; onChange: (view: InspectionView) => void }) {
  return <div className="inspection-view-selector" role="group" aria-label="Visualização das inspeções">
    {inspectionViewOptions.map((option) => <button key={option.value} type="button" aria-controls="inspection-collection" aria-pressed={view === option.value} onClick={() => onChange(option.value)}>{option.label}</button>)}
  </div>;
}

export function InspectionViews({ inspections, view, actions }: { inspections: InspectionRecord[]; view: InspectionView; actions?: InspectionActionHandlers }) {
  if (view === "quadro") return <InspectionBoard inspections={inspections} actions={actions} />;
  if (view === "agenda") return <InspectionAgenda inspections={inspections} actions={actions} />;
  return <InspectionList inspections={inspections} actions={actions} />;
}

function InspectionList({ inspections, actions }: { inspections: InspectionRecord[]; actions?: InspectionActionHandlers }) {
  return inspections.length ? <div className="inspection-table-box"><table className="inspection-table"><thead><tr><th>Inspeção</th><th>Situação</th><th>Vencimento</th><th>Ação</th></tr></thead><tbody>{inspections.map((inspection) => <tr key={inspection.id} data-inspection-id={inspection.id}><td data-label="Inspeção"><strong>{inspectionName(inspection)}</strong><span>{inspection.source || "Origem não informada"} · {inspection.evidenceCount} evidência(s) · v{inspection.version}</span></td><td data-label="Situação"><InspectionStatus status={inspection.status} /></td><td data-label="Vencimento"><strong>{formatInspectionDate(inspection.dueAt)}</strong><span>Prazo final · {formatInspectionDate(inspection.deadlineAt)}</span></td><td data-label="Ação"><Link href={`/inspections?inspectionId=${encodeURIComponent(inspection.id)}`} className="inspection-open-link">Abrir inspeção →</Link><InspectionActions inspection={inspection} actions={actions} /></td></tr>)}</tbody></table></div> : <EmptyInspections />;
}

function InspectionBoard({ inspections, actions }: { inspections: InspectionRecord[]; actions?: InspectionActionHandlers }) {
  const groups = groupInspectionsByStatus(inspections);
  return <div className="inspection-board">{inspectionColumns.map((column) => <section className="inspection-column" key={column.key} aria-labelledby={`inspection-column-${column.key}`}><h2 id={`inspection-column-${column.key}`}>{column.label}<span aria-label={`${groups[column.key].length} inspeções`}>{groups[column.key].length}</span></h2>{groups[column.key].length ? <div className="inspection-column-list">{groups[column.key].map((inspection) => <InspectionCompactCard key={inspection.id} inspection={inspection} actions={actions} />)}</div> : <p className="inspection-column-empty">Nenhuma inspeção nesta situação.</p>}</section>)}</div>;
}

function InspectionAgenda({ inspections, actions }: { inspections: InspectionRecord[]; actions?: InspectionActionHandlers }) {
  const sorted = sortInspectionsByDueAt(inspections);
  return sorted.length ? <div className="inspection-agenda-layout"><ol className="inspection-agenda">{sorted.map((inspection) => <li key={inspection.id}><div className="inspection-agenda-date"><span>Vencimento</span><strong>{formatAgendaDay(inspection.dueAt)}</strong><small>{formatInspectionStatus(inspection.status)}</small></div><InspectionCompactCard inspection={inspection} actions={actions} /></li>)}</ol><aside className="inspection-agenda-context"><span>Leitura da agenda</span><h2>Do prazo à ação.</h2><p>Selecione um registro para consultar a situação, o vencimento e o prazo final.</p><hr /><p>A triagem continua disponível para acompanhar a classificação das inspeções.</p><Link href="/triage">Abrir triagem →</Link></aside></div> : <EmptyInspections />;
}

function InspectionCompactCard({ inspection, actions }: { inspection: InspectionRecord; actions?: InspectionActionHandlers }) {
  return <article className="inspection-record-card" data-inspection-id={inspection.id}>
    <span className="inspection-record-kicker">{inspection.source || "Origem não informada"}</span>
    <Link href={`/inspections?inspectionId=${encodeURIComponent(inspection.id)}`} className="inspection-record-title">{inspectionName(inspection)}</Link>
    <span>Participante · {shortId(inspection.participantId)}</span>
    <span>Vencimento · {formatInspectionDate(inspection.dueAt)}</span>
    <span>{inspection.evidenceCount} evidência(s) · prazo final {formatInspectionDate(inspection.deadlineAt)} · v{inspection.version}</span>
    <InspectionActions inspection={inspection} actions={actions} />
  </article>;
}

function InspectionStatus({ status }: { status: string }) {
  return <span className="inspection-status" title={status || "UNKNOWN"}>{formatInspectionStatus(status)}</span>;
}

function InspectionActions({ inspection, actions }: { inspection: InspectionRecord; actions?: InspectionActionHandlers }) {
  if (!actions) return null;
  return <details className="inspection-action-menu"><summary>Ações</summary><div><button type="button" onClick={() => actions.onCancel(inspection)}>Cancelar</button><button type="button" className="secondary" onClick={() => actions.onInvalidate(inspection)}>Invalidar</button><button type="button" className="secondary" onClick={() => actions.onRecapture(inspection)}>Solicitar recaptura</button></div></details>;
}

function EmptyInspections() {
  return <p role="status">Nenhuma inspeção encontrada neste escopo.</p>;
}

function parseDate(value: string | null | undefined): Date | undefined {
  if (!value) return undefined;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? undefined : date;
}

function formatAgendaDay(value: string | null | undefined): string {
  const date = parseDate(value);
  return date ? new Intl.DateTimeFormat("pt-BR", { day: "2-digit", month: "2-digit" }).format(date) : value ? "Data inválida" : "Sem data";
}

function inspectionName(inspection: InspectionRecord): string {
  return `Inspeção ${shortId(inspection.id)}`;
}

function shortId(value: string): string {
  return value.length > 12 ? `${value.slice(0, 8)}…` : value;
}
