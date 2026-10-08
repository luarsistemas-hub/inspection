import Link from "next/link";
import { IconButton } from "@inspection/design-system";
import { useCallback, useEffect, useState } from "react";
import { getMembershipId } from "@/auth/session";
import type { InspectionsQuery } from "@/graphql/generated";
import { presentDashboardStatus, presentInspectionSource } from "./presentation";

export type InspectionView = "lista" | "quadro" | "agenda";
export type InspectionRecord = InspectionsQuery["inspections"]["nodes"][number];
export function canRequestInspectionRecapture(inspection: Pick<InspectionRecord, "evidenceCount" | "status">): boolean {
  return inspection.evidenceCount > 0 && (inspection.status === "SUBMITTED" || inspection.status === "COMPLETED");
}
const defaultInspectionView: InspectionView = "lista";
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
  RECAPTURE_PENDING: "Complemento solicitado",
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
  if (typeof window === "undefined") return defaultInspectionView;
  try {
    const stored = window.localStorage.getItem(inspectionViewStorageKey(membershipId));
    return isInspectionView(stored) ? stored : defaultInspectionView;
  } catch {
    return defaultInspectionView;
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
    const searchable = [inspection.id, inspection.assetId, inspection.assetName, inspection.assetAddress, inspection.assetExternalKey, inspection.participantId, inspection.participantName, inspection.source, inspection.status].join(" ").toLocaleLowerCase("pt-BR");
    return matchesColumn && (!normalizedQuery || searchable.includes(normalizedQuery));
  });
}

export function formatInspectionStatus(status: string): string {
  return presentDashboardStatus(status);
}

export function formatInspectionDate(value: string | null | undefined): string {
  if (!value) return "Data não informada";
  const date = parseDate(value);
  if (!date) return "Data inválida";
  return new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short" }).format(date);
}

export function useInspectionView(): [InspectionView, (view: InspectionView) => void] {
  const membershipId = getMembershipId();
  const [view, setView] = useState<InspectionView>(defaultInspectionView);

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
  return <div className="inspection-view-selector" role="group" aria-label="Visualização das vistorias">
    {inspectionViewOptions.map((option) => <button key={option.value} type="button" aria-controls="inspection-collection" aria-pressed={view === option.value} onClick={() => onChange(option.value)}>{option.label}</button>)}
  </div>;
}

export function InspectionViews({ inspections, view, actions, onOpen, focusedId, onFocus }: { inspections: InspectionRecord[]; view: InspectionView; actions?: InspectionActionHandlers; onOpen: (inspection: InspectionRecord) => void; focusedId?: string; onFocus?: (id: string) => void }) {
  if (view === "quadro") return <InspectionBoard inspections={inspections} actions={actions} />;
  if (view === "agenda") return <InspectionAgenda inspections={inspections} actions={actions} focusedId={focusedId} onFocus={onFocus} />;
  return <InspectionList inspections={inspections} actions={actions} onOpen={onOpen} focusedId={focusedId} onFocus={onFocus} />;
}

function InspectionList({ inspections, actions, onOpen, focusedId, onFocus }: { inspections: InspectionRecord[]; actions?: InspectionActionHandlers; onOpen: (inspection: InspectionRecord) => void; focusedId?: string; onFocus?: (id: string) => void }) {
  if (!inspections.length) return <EmptyInspections />;
  return <ol className="inspection-focus-rows" aria-label="Vistorias">{inspections.map((inspection) => <li key={inspection.id} className="inspection-focus-row" data-focused={focusedId === inspection.id} data-inspection-id={inspection.id}>
    <div className="inspection-focus-row-main">{onFocus ? <><button className="inspection-focus-select" type="button" aria-pressed={focusedId === inspection.id} onClick={() => onFocus(inspection.id)}>{inspectionName(inspection)}</button><Link className="inspection-focus-mobile-link" href={`/inspections?inspectionId=${encodeURIComponent(inspection.id)}`}>{inspectionName(inspection)}</Link></> : <Link href={`/inspections?inspectionId=${encodeURIComponent(inspection.id)}`}>{inspectionName(inspection)}</Link>}<span>Responsável · {inspection.participantName?.trim() || "indisponível"}</span></div>
    <div className="inspection-focus-row-side"><time dateTime={inspection.dueAt || undefined}>{formatInspectionDate(inspection.dueAt)}</time><InspectionStatus status={inspection.status} /><IconButton label={`Abrir detalhes da vistoria ${inspectionName(inspection)}`} tooltip="Abrir detalhes" icon="eye" onPress={() => onOpen(inspection)} />{inspection.status === "COMPLETED" && <Link href={`/reports?inspectionId=${encodeURIComponent(inspection.id)}`} className="inspection-open-link">Abrir laudo</Link>}<InspectionActions inspection={inspection} actions={actions} /></div>
  </li>)}</ol>;
}

function InspectionBoard({ inspections, actions }: { inspections: InspectionRecord[]; actions?: InspectionActionHandlers }) {
  const groups = groupInspectionsByStatus(inspections);
  const [mobileColumn, setMobileColumn] = useState<InspectionColumnKey>("planejamento");
  return <><div className="inspection-board-mobile-tabs" role="group" aria-label="Etapa do quadro">{inspectionColumns.map((column) => <button key={column.key} type="button" aria-pressed={mobileColumn === column.key} onClick={() => setMobileColumn(column.key)}>{column.label} · {groups[column.key].length}</button>)}</div><div className="inspection-board">{inspectionColumns.map((column) => <section className="inspection-column" data-mobile-active={mobileColumn === column.key} key={column.key} aria-labelledby={`inspection-column-${column.key}`}><h2 id={`inspection-column-${column.key}`}>{column.label}<span aria-label={`${groups[column.key].length} vistorias`}>{groups[column.key].length}</span></h2>{groups[column.key].length ? <div className="inspection-column-list">{groups[column.key].map((inspection) => <InspectionCompactCard key={inspection.id} inspection={inspection} actions={actions} compact />)}</div> : <p className="inspection-column-empty">Nenhuma vistoria nesta situação.</p>}</section>)}</div></>;
}

function InspectionAgenda({ inspections, actions, focusedId, onFocus }: { inspections: InspectionRecord[]; actions?: InspectionActionHandlers; focusedId?: string; onFocus?: (id: string) => void }) {
  const sorted = sortInspectionsByDueAt(inspections);
  const groups = new Map<string, InspectionRecord[]>();
  for (const inspection of sorted) {
    const date = parseDate(inspection.dueAt);
    const day = date ? new Intl.DateTimeFormat("pt-BR", { dateStyle: "long" }).format(date) : "Sem data";
    groups.set(day, [...(groups.get(day) ?? []), inspection]);
  }
  return sorted.length ? <div className="inspection-agenda-layout"><ol className="inspection-agenda">{[...groups].map(([day, items]) => <li className="inspection-agenda-group" key={day}><h2>{day} · {items.length} {items.length === 1 ? "vistoria" : "vistorias"}</h2><ol>{items.map((inspection) => <li key={inspection.id} className="inspection-agenda-item"><time dateTime={inspection.dueAt}>{parseDate(inspection.dueAt) ? new Intl.DateTimeFormat("pt-BR", { hour: "2-digit", minute: "2-digit" }).format(new Date(inspection.dueAt)) : "—"}</time><InspectionCompactCard inspection={inspection} actions={actions} focused={focusedId === inspection.id} onFocus={onFocus} compact /></li>)}</ol></li>)}</ol></div> : <EmptyInspections />;
}

function InspectionCompactCard({ inspection, actions, focused, onFocus, compact = false }: { inspection: InspectionRecord; actions?: InspectionActionHandlers; focused?: boolean; onFocus?: (id: string) => void; compact?: boolean }) {
  return <article className={`inspection-record-card${compact ? " inspection-record-card--focus" : ""}`} data-focused={focused} data-inspection-id={inspection.id}>
    {!compact && <span className="inspection-record-kicker">{presentInspectionSource(inspection.source)}</span>}
    {onFocus ? <><button className="inspection-focus-select inspection-record-title" type="button" aria-pressed={focused} onClick={() => onFocus(inspection.id)}>{inspectionName(inspection)}</button><Link href={`/inspections?inspectionId=${encodeURIComponent(inspection.id)}`} className="inspection-focus-mobile-link inspection-record-title">{inspectionName(inspection)}</Link></> : <Link href={`/inspections?inspectionId=${encodeURIComponent(inspection.id)}`} className="inspection-record-title">{inspectionName(inspection)}</Link>}
    <span>Responsável · {inspection.participantName?.trim() || "indisponível"}</span>
    {compact && !onFocus && <><span>{formatInspectionDate(inspection.dueAt)}</span><span className="inspection-status">{formatInspectionStatus(inspection.status)}</span></>}
    {!compact && inspection.assetAddress && <span>{inspection.assetAddress}</span>}
    {!compact && <span>Vencimento · {formatInspectionDate(inspection.dueAt)}</span>}
    {!compact && <span>{inspection.evidenceCount} evidência(s) · prazo final {formatInspectionDate(inspection.deadlineAt)} · v{inspection.version}</span>}
    {inspection.status === "COMPLETED" && <Link href={`/reports?inspectionId=${encodeURIComponent(inspection.id)}`} className="inspection-open-link">Abrir laudo →</Link>}
    <InspectionActions inspection={inspection} actions={actions} />
  </article>;
}

function InspectionStatus({ status }: { status: string }) {
  return <span className="inspection-status" title={formatInspectionStatus(status)}>{formatInspectionStatus(status)}</span>;
}

function InspectionActions({ inspection, actions }: { inspection: InspectionRecord; actions?: InspectionActionHandlers }) {
  if (!actions) return null;
  return <details className="inspection-action-menu"><summary>Ações</summary><div><button type="button" onClick={() => actions.onCancel(inspection)}>Cancelar</button><button type="button" className="secondary" onClick={() => actions.onInvalidate(inspection)}>Invalidar</button>{canRequestInspectionRecapture(inspection) && <button type="button" className="secondary" onClick={() => actions.onRecapture(inspection)}>Solicitar complemento</button>}</div></details>;
}

function EmptyInspections() {
  return <p role="status">Nenhuma vistoria encontrada nesta abrangência.</p>;
}

function parseDate(value: string | null | undefined): Date | undefined {
  if (!value) return undefined;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? undefined : date;
}

function inspectionName(inspection: InspectionRecord): string {
  return inspection.assetName?.trim() || "Imóvel indisponível";
}
