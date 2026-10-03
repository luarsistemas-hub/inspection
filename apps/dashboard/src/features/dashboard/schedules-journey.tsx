"use client";

import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { FormDialog } from "./form-dialog";
import { ConfirmationDialog } from "./confirmation-dialog";
import { EntityRelationshipField, type FormOptionsState } from "./form-options";
import {
  CancelScheduleDocument,
  CreateScheduleDocument,
  InspectionsDocument,
  SchedulesDocument,
  UpdateScheduleDocument,
  type InspectionsQuery,
  type SchedulesQuery,
} from "@/graphql/generated";
import { graphql, type GraphQLFailure } from "@/graphql/client";
import { presentDashboardStatus } from "./presentation";

const scheduleTimezone = "America/Sao_Paulo";
const frequencies = [
  { value: "DAILY", label: "Diariamente" },
  { value: "WEEKLY", label: "Semanalmente" },
  { value: "MONTHLY", label: "Mensalmente" },
  { value: "YEARLY", label: "Anualmente" },
] as const;

type Schedule = SchedulesQuery["schedules"]["nodes"][number];
type Inspection = InspectionsQuery["inspections"]["nodes"][number];
type ScheduleView = "day" | "week" | "rules";

export function SchedulesJourney({
  canMutate,
  options,
  refreshKey,
}: {
  canMutate: boolean;
  options: FormOptionsState;
  refreshKey: number;
}) {
  const [data, setData] = useState<SchedulesQuery>();
  const [inspections, setInspections] = useState<Inspection[]>([]);
  const [message, setMessage] = useState("Carregando agendas…");
  const [inspectionMessage, setInspectionMessage] = useState("Carregando vistorias…");
  const [view, setView] = useState<ScheduleView>("day");
  const [selectedDate, setSelectedDate] = useState(() => dateKeyInTimezone(new Date()));
  const [participantFilter, setParticipantFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [createOpen, setCreateOpen] = useState(false);
  const [editItem, setEditItem] = useState<Schedule>();
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState(false);
  const [createError, setCreateError] = useState("");
  const [editError, setEditError] = useState("");
  const [cancelItem, setCancelItem] = useState<Schedule>();
  const busyRef = useRef(false);
  const openedScheduleRef = useRef<string | undefined>(undefined);
  const scheduleId = useSearchParams().get("scheduleId");

  const loadInspections = useCallback(async () => {
    setInspectionMessage("Atualizando vistorias…");
    try {
      const result = await graphql<InspectionsQuery, { first: number; after: string | null; history: boolean; search: string | null; statusGroup: null }>(
        InspectionsDocument,
        { first: 100, after: null, history: false, search: null, statusGroup: null },
      );
      setInspections(result.inspections.nodes.filter((item) => !["INVALIDATED", "CANCELED"].includes(item.status)));
      setInspectionMessage("Vistorias atualizadas.");
    } catch {
      setInspectionMessage("Não foi possível carregar as vistorias. Tente atualizar a agenda.");
    }
  }, []);

  const load = useCallback(async (targetScheduleId?: string) => {
    try {
      const next = await graphql<SchedulesQuery, { after: string | null }>(SchedulesDocument, { after: null });
      let schedules = next.schedules;
      while (targetScheduleId && !schedules.nodes.some((item) => item.id === targetScheduleId) && schedules.pageInfo.hasNextPage && schedules.pageInfo.endCursor) {
        const page = await graphql<SchedulesQuery, { after: string | null }>(SchedulesDocument, { after: schedules.pageInfo.endCursor });
        schedules = { ...page.schedules, nodes: [...schedules.nodes, ...page.schedules.nodes.filter((item) => !schedules.nodes.some((existing) => existing.id === item.id))] };
      }
      setData({ ...next, schedules });
      setMessage(targetScheduleId && !schedules.nodes.some((item) => item.id === targetScheduleId) ? "Esta agenda não está disponível neste contexto." : schedules.nodes.length ? "Agendas atualizadas." : "Nenhuma agenda encontrada.");
    } catch (error) {
      setMessage(formatFailure(error));
    }
  }, []);

  useEffect(() => { void load(scheduleId ?? undefined); void loadInspections(); }, [load, loadInspections, scheduleId, refreshKey]);
  useEffect(() => {
    if (!scheduleId) { openedScheduleRef.current = undefined; return; }
    if (openedScheduleRef.current === scheduleId) return;
    const item = data?.schedules.nodes.find((schedule) => schedule.id === scheduleId);
    if (item) { openedScheduleRef.current = scheduleId; setEditItem(item); }
  }, [scheduleId, data]);

  const mutate = async (
    document: typeof CreateScheduleDocument | typeof UpdateScheduleDocument | typeof CancelScheduleDocument,
    input: Record<string, unknown>,
  ): Promise<string | undefined> => {
    try {
      const result = await graphql(document as never, { input } as never);
      const payload = Object.values(result as Record<string, unknown>)[0] as { userErrors?: Array<{ message: string; code: string }> };
      if (payload.userErrors?.length) {
        setMessage(formatMutationErrors(payload.userErrors));
        return formatMutationErrors(payload.userErrors);
      }
      setMessage("Agenda salva. Atualizando dados…");
      const createdScheduleId = document === CreateScheduleDocument
        ? (payload as { schedule?: { id?: string } | null }).schedule?.id
        : undefined;
      await load(createdScheduleId);
      await loadInspections();
      return undefined;
    } catch (error) {
      setMessage(formatFailure(error));
      return formatFailure(error);
    }
  };

  const create = async (input: Record<string, unknown>) => {
    if (busyRef.current) return;
    busyRef.current = true; setCreating(true); setCreateError("");
    const error = await mutate(CreateScheduleDocument, input);
    busyRef.current = false; setCreating(false);
    if (error) setCreateError(error); else setCreateOpen(false);
  };

  const update = async (item: Schedule, input: ScheduleUpdateValues) => {
    if (busyRef.current) return;
    busyRef.current = true; setEditing(true); setEditError("");
    const error = await mutate(UpdateScheduleDocument, {
      scheduleId: item.id,
      expectedVersion: item.version,
      clientMutationId: mutationId(),
      rrule: input.rrule,
      timezone: item.timezone,
      startsAt: input.startsAt,
      deadlineMinutes: input.deadlineMinutes,
      reminderOffsetsMinutes: input.reminderOffsetsMinutes,
    });
    busyRef.current = false; setEditing(false);
    if (error) setEditError(error); else setEditItem(undefined);
  };

  const cancel = async (item: Schedule) => {
    return mutate(CancelScheduleDocument, {
      scheduleId: item.id,
      expectedVersion: item.version,
      clientMutationId: mutationId(),
    });
  };

  const activeSchedules = data?.schedules.nodes.filter((item) => item.status === "ACTIVE") ?? [];
  const visibleInspections = inspections.filter((item) => {
    const day = dateKeyInTimezone(new Date(item.dueAt));
    const dateMatches = view === "day" ? day === selectedDate : view === "week" && dateInWeek(day, selectedDate);
    return dateMatches
      && (!participantFilter || item.participantId === participantFilter)
      && (!statusFilter || item.status === statusFilter);
  }).sort((left, right) => Date.parse(left.dueAt) - Date.parse(right.dueAt));
  const weekDays = Array.from({ length: 7 }, (_, index) => shiftDateKey(startOfWeek(selectedDate), index));
  const assetNames = new Map(options.data?.assets.nodes.map((item) => [item.id, item.name]) ?? []);
  const participantNames = new Map(options.data?.participants.nodes.map((item) => [item.id, item.name]) ?? []);
  const moveDate = (amount: number) => setSelectedDate((current) => shiftDateKey(current, amount * (view === "week" ? 7 : 1)));

  return <div className="feature schedules-journey">
    <div className="schedules-toolbar">
      <div className="schedules-tabs" role="group" aria-label="Visualização da agenda">
        {([ ["day", "Dia"], ["week", "Semana"], ["rules", "Recorrências"] ] as const).map(([value, label]) => <button key={value} type="button" aria-pressed={view === value} onClick={() => setView(value)}>{label}</button>)}
      </div>
      {canMutate && <button onClick={() => { setCreateError(""); setCreateOpen(true); }}>{view === "rules" ? "Nova recorrência" : "Nova agenda"}</button>}
    </div>

    {view !== "rules" && <section className="schedules-calendar" aria-labelledby="schedules-calendar-title">
      <div className="schedules-calendar-heading">
        <div><h2 id="schedules-calendar-title">{view === "day" ? weekdayDate(selectedDate) : weekRangeLabel(weekDays[0], weekDays[6])}</h2><p>{inspectionMessage}</p></div>
        <div className="actions"><button className="secondary" type="button" onClick={() => setSelectedDate(dateKeyInTimezone(new Date()))}>Hoje</button><button className="secondary" type="button" aria-label="Período anterior" onClick={() => moveDate(-1)}>‹</button><button className="secondary" type="button" aria-label="Próximo período" onClick={() => moveDate(1)}>›</button><label className="schedule-date-picker">Data<input aria-label="Data da agenda" type="date" value={selectedDate} onChange={(event) => setSelectedDate(event.target.value)} /></label></div>
      </div>
      <div className="schedules-filter-row">
        <label>Responsável<select aria-label="Filtrar por responsável" value={participantFilter} onChange={(event) => setParticipantFilter(event.target.value)}><option value="">Todos</option>{(options.data?.participants.nodes ?? []).map((participant) => <option key={participant.id} value={participant.id}>{participant.name}</option>)}</select></label>
        <label>Situação<select aria-label="Filtrar por situação" value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}><option value="">Todas</option>{[...new Set(inspections.map((item) => item.status))].map((status) => <option key={status} value={status}>{presentDashboardStatus(status)}</option>)}</select></label>
        {activeSchedules.length > 0 && <span>{activeSchedules.length} recorrência(s) ativa(s) · <button className="schedule-inline-link" type="button" onClick={() => setView("rules")}>Ver recorrências</button></span>}
      </div>
      {view === "day" && <InspectionList items={visibleInspections} />}
      {view === "week" && <div className="schedules-week-grid">{weekDays.map((day) => <section className="schedules-week-day" key={day}><h3>{weekdayDate(day)}</h3><InspectionList items={visibleInspections.filter((item) => dateKeyInTimezone(new Date(item.dueAt)) === day)} compact /></section>)}</div>}
    </section>}

    {view === "rules" && <section className="schedules-list" aria-labelledby="schedules-list-title">
      <div className="schedules-list-heading"><div><h2 id="schedules-list-title">Recorrências cadastradas</h2><p>Estas são próximas datas previstas; a vistoria aparece na agenda quando estiver confirmada.</p></div><button className="secondary" onClick={() => { void load(); void loadInspections(); }}>Atualizar agenda</button></div>
      <p role="status" aria-live="polite">{message}</p>
      {data && activeSchedules.length === 0 && <p className="schedules-empty">Nenhuma recorrência ativa. Agende vistorias recorrentes para organizar a rotina.</p>}
      {data && activeSchedules.length > 0 && <ul className="collection schedules-collection">
        {activeSchedules.map((item) => <li key={item.id}>
          <div className="schedule-summary">
            <strong>{assetNames.get(item.assetId) || "Imóvel sem nome disponível"}</strong>
            <span>Responsável · {participantNames.get(item.participantId) || "Responsável não disponível"}</span>
            <span>Próxima ocorrência prevista · {formatScheduleDateTime(item.nextDueAt)}</span>
            <span>Repetição · {presentScheduleFrequency(item.rrule)}</span>
            <span>Prazo · {item.deadlineMinutes} minutos · lembretes {presentReminders(item.reminderOffsetsMinutes)}</span>
          </div>
          {canMutate && <div className="actions"><button className="secondary" onClick={() => { setEditError(""); setEditItem(item); }}>Editar recorrência</button><button className="secondary" onClick={() => setCancelItem(item)}>Cancelar recorrência</button></div>}
        </li>)}
      </ul>}
    </section>}
    {view === "rules" && <p className="schedules-calendar-status" role="status">{inspectionMessage}</p>}
    {canMutate && <>
      <FormDialog isOpen={createOpen} onClose={() => setCreateOpen(false)} title="Nova agenda" busy={creating} error={createError}>
        {options.loading && <p role="status">Carregando imóveis, responsáveis pela vistoria e modelos de vistoria…</p>}
        {options.error && <p className="warning" role="alert">Não foi possível carregar as opções do cadastro. {options.error}</p>}
        {options.data && <CreateScheduleForm options={options} onSubmit={(input) => void create(input)} busy={creating} />}
      </FormDialog>
      <FormDialog isOpen={Boolean(editItem)} onClose={() => setEditItem(undefined)} title="Editar agenda" busy={editing} error={editError}>
        {editItem && <EditScheduleForm key={`${editItem.id}:${editItem.version}`} item={editItem} onSubmit={(input) => void update(editItem, input)} busy={editing} />}
      </FormDialog>
      <ConfirmationDialog isOpen={Boolean(cancelItem)} onClose={() => setCancelItem(undefined)} title="Cancelar agenda" target="Agenda selecionada" scope="Contexto operacional atual" consequence="A agenda será cancelada; as vistorias históricas serão preservadas." confirmLabel="Cancelar agenda" onConfirm={async () => { if (!cancelItem) return; const failure = await cancel(cancelItem); if (failure) throw new Error(failure); }} />
    </>}
  </div>;
}

function InspectionList({ items, compact = false }: { items: Inspection[]; compact?: boolean }) {
  if (!items.length) return <p className="schedules-calendar-empty">Nenhuma vistoria confirmada neste período.</p>;
  return <ul className={`schedules-calendar-events${compact ? " schedules-calendar-events--compact" : ""}`}>
    {items.map((item) => <li className="schedules-calendar-event" key={item.id}>
      <time dateTime={item.dueAt}>{new Intl.DateTimeFormat("pt-BR", { hour: "2-digit", minute: "2-digit", timeZone: scheduleTimezone }).format(new Date(item.dueAt))}</time>
      <div><strong>{item.assetName?.trim() || "Imóvel indisponível"}</strong>{item.assetAddress && <span>{item.assetAddress}</span>}<span>Responsável · {item.participantName?.trim() || "Indisponível"}</span></div>
      <div className="schedules-calendar-event-side"><span className="home-status">{presentDashboardStatus(item.status)}</span><Link href={`/inspections?inspectionId=${encodeURIComponent(item.id)}`}>Abrir vistoria →</Link></div>
    </li>)}
  </ul>;
}

function dateKeyInTimezone(date: Date): string {
  const parts = new Intl.DateTimeFormat("en-CA", { timeZone: scheduleTimezone, year: "numeric", month: "2-digit", day: "2-digit" }).formatToParts(date);
  const values = Object.fromEntries(parts.map((part) => [part.type, part.value]));
  return `${values.year}-${values.month}-${values.day}`;
}

function shiftDateKey(value: string, amount: number): string {
  const date = new Date(`${value}T12:00:00Z`);
  date.setUTCDate(date.getUTCDate() + amount);
  return date.toISOString().slice(0, 10);
}

function startOfWeek(value: string): string {
  const day = new Date(`${value}T12:00:00Z`).getUTCDay();
  return shiftDateKey(value, -((day + 6) % 7));
}

function dateInWeek(day: string, selected: string): boolean {
  const start = startOfWeek(selected);
  return day >= start && day <= shiftDateKey(start, 6);
}

function weekdayDate(value: string): string {
  const date = new Date(`${value}T12:00:00-03:00`);
  return new Intl.DateTimeFormat("pt-BR", { weekday: "long", day: "numeric", month: "long", timeZone: scheduleTimezone }).format(date);
}

function weekRangeLabel(first: string, last: string): string {
  const start = new Date(`${first}T12:00:00-03:00`);
  const end = new Date(`${last}T12:00:00-03:00`);
  const formatter = new Intl.DateTimeFormat("pt-BR", { day: "numeric", month: "short", timeZone: scheduleTimezone });
  return `${formatter.format(start)} – ${formatter.format(end)}`;
}

function FormOptionsNotice({ options }: { options: { loading: boolean; error?: string } }) {
  if (options.loading) return <p role="status">Carregando imóveis, responsáveis pela vistoria e modelos de vistoria…</p>;
  if (options.error) return <p className="warning" role="alert">Não foi possível carregar as opções do cadastro. {options.error}</p>;
  return null;
}

function CreateScheduleForm({ options, onSubmit, busy }: { options: FormOptionsState; onSubmit: (input: Record<string, unknown>) => void; busy: boolean }) {
  const [assetId, setAssetId] = useState("");
  const [participantId, setParticipantId] = useState("");
  const [templateId, setTemplateId] = useState("");
  const [comparisonMode, setComparisonMode] = useState("CHECKLIST_ONLY");
  const [frequency, setFrequency] = useState("MONTHLY");
  const [startsAt, setStartsAt] = useState("");
  const [deadlineMinutes, setDeadlineMinutes] = useState("60");
  const [reminders, setReminders] = useState(["30"]);
  const [validationError, setValidationError] = useState("");
  const asset = options.data?.assets.nodes.find((item) => item.id === assetId);

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const settings = validateScheduleSettings(deadlineMinutes, reminders);
    if (settings.error) { setValidationError(settings.error); return; }
    setValidationError("");
    onSubmit({
      assetId,
      participantId,
      templateId,
      comparisonMode,
      rrule: `FREQ=${frequency}`,
      startsAt,
      timezone: scheduleTimezone,
      deadlineMinutes: settings.deadlineMinutes,
      reminderOffsetsMinutes: settings.reminders,
      referenceVersionId: null,
      clientMutationId: mutationId(),
    });
  };

  return <form className="schedule-form" onSubmit={submit}>
    <fieldset className="schedule-form-section">
      <legend>Dados da vistoria</legend>
      <EntityRelationshipField kind="asset" label="Imóvel" value={assetId} onChange={(value) => { setAssetId(value); setParticipantId(""); setTemplateId(""); }} options={options} required />
      <EntityRelationshipField kind="participant" label="Responsável pela vistoria" value={participantId} onChange={setParticipantId} options={options} required disabled={!assetId} />
      <EntityRelationshipField kind="template" label="Modelo de vistoria" value={templateId} onChange={setTemplateId} options={options} segmentVersionId={asset?.segmentVersionId} required disabled={!assetId} />
      <label>Comparação<select aria-label="Comparação" value={comparisonMode} onChange={(event) => setComparisonMode(event.target.value)}><option value="CHECKLIST_ONLY">Somente checklist</option><option value="FIXED_ORIGIN">Comparar com fotos de referência do imóvel</option></select></label>
    </fieldset>
    <fieldset className="schedule-form-section">
      <legend>Quando acontece</legend>
      <label>Primeira vistoria<input aria-label="Primeira vistoria" type="datetime-local" value={startsAt} required onChange={(event) => setStartsAt(event.target.value)} /></label>
      <label>Repetir<select aria-label="Repetir" value={frequency} onChange={(event) => setFrequency(event.target.value)}>{frequencies.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}</select></label>
    </fieldset>
    <ScheduleNotices deadlineMinutes={deadlineMinutes} onDeadlineChange={setDeadlineMinutes} reminders={reminders} onRemindersChange={setReminders} validationError={validationError} />
    <button type="submit" disabled={busy}>Criar agenda</button>
  </form>;
}

type ScheduleUpdateValues = { rrule: string; startsAt: string; deadlineMinutes: number; reminderOffsetsMinutes: number[] };

function EditScheduleForm({ item, onSubmit, busy }: { item: Schedule; onSubmit: (input: ScheduleUpdateValues) => void; busy: boolean }) {
  const initialFrequency = scheduleFrequencyValue(item.rrule);
  const isBrazilTimezone = item.timezone === scheduleTimezone;
  const [frequency, setFrequency] = useState(initialFrequency ?? "CUSTOM");
  const [startsAt, setStartsAt] = useState(isBrazilTimezone ? instantToLocalDateTime(item.startsAt, scheduleTimezone) : "");
  const [deadlineMinutes, setDeadlineMinutes] = useState(String(item.deadlineMinutes));
  const [reminders, setReminders] = useState(item.reminderOffsetsMinutes.map(String));
  const [validationError, setValidationError] = useState("");

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const settings = validateScheduleSettings(deadlineMinutes, reminders);
    if (settings.error) { setValidationError(settings.error); return; }
    setValidationError("");
    onSubmit({
      ...scheduleEditValues(item, frequency, startsAt),
      deadlineMinutes: settings.deadlineMinutes,
      reminderOffsetsMinutes: settings.reminders,
    });
  };

  return <form className="schedule-edit-form" onSubmit={submit}>
    <fieldset className="schedule-form-section">
      <legend>Quando acontece</legend>
      {isBrazilTimezone
        ? <label>Primeira vistoria<input aria-label="Primeira vistoria" type="datetime-local" value={startsAt} required onChange={(event) => setStartsAt(event.target.value)} /></label>
        : <div className="schedule-readonly-start"><strong>Início</strong><span>{formatScheduleDateTime(item.startsAt)}</span><small>O início desta agenda antiga não pode ser alterado por aqui.</small></div>}
      <label>Repetir<select aria-label="Repetir" value={frequency} onChange={(event) => setFrequency(event.target.value)}>
        {initialFrequency === null && <option value="CUSTOM">Personalizada (atual)</option>}
        {frequencies.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
      </select></label>
    </fieldset>
    <ScheduleNotices deadlineMinutes={deadlineMinutes} onDeadlineChange={setDeadlineMinutes} reminders={reminders} onRemindersChange={setReminders} validationError={validationError} />
    {initialFrequency === null && <p className="schedule-custom-rule-note">A repetição personalizada será mantida enquanto uma frequência da lista não for escolhida.</p>}
    <button type="submit" disabled={busy}>Salvar alterações</button>
  </form>;
}

function ScheduleNotices({
  deadlineMinutes,
  onDeadlineChange,
  reminders,
  onRemindersChange,
  validationError,
}: {
  deadlineMinutes: string;
  onDeadlineChange: (value: string) => void;
  reminders: string[];
  onRemindersChange: (value: string[]) => void;
  validationError: string;
}) {
  return <fieldset className="schedule-form-section schedule-notices">
    <legend>Avisos e prazo</legend>
    <label>Prazo para concluir (minutos)<input aria-label="Prazo para concluir (minutos)" type="number" min="1" step="1" value={deadlineMinutes} required onChange={(event) => onDeadlineChange(event.target.value)} /></label>
    <fieldset className="schedule-reminders">
      <legend>Lembretes</legend>
      <p>Minutos após o início da vistoria. Você pode remover todos.</p>
      {reminders.map((value, index) => <div className="schedule-reminder-row" key={index}>
        <label>Lembrete {index + 1} (minutos após o início)<input aria-label={`Lembrete ${index + 1} (minutos após o início)`} type="number" min="0" step="1" value={value} required onChange={(event) => onRemindersChange(reminders.map((reminder, reminderIndex) => reminderIndex === index ? event.target.value : reminder))} /></label>
        <button type="button" className="secondary" aria-label={`Remover lembrete ${index + 1}`} onClick={() => onRemindersChange(reminders.filter((_, reminderIndex) => reminderIndex !== index))}>Remover</button>
      </div>)}
      {reminders.length < 3 && <button type="button" className="secondary schedule-add-reminder" onClick={() => onRemindersChange([...reminders, ""])}>Adicionar lembrete</button>}
    </fieldset>
    {validationError && <p className="schedule-validation-error" role="alert">{validationError}</p>}
  </fieldset>;
}

export function scheduleFrequencyValue(rrule: string): string | null {
  const match = /^FREQ=(DAILY|WEEKLY|MONTHLY|YEARLY)$/i.exec(rrule.trim());
  return match?.[1].toUpperCase() ?? null;
}

export function scheduleEditValues(item: Pick<Schedule, "rrule" | "timezone" | "startsAt">, frequency: string, startsAt: string) {
  return {
    rrule: frequency === "CUSTOM" ? item.rrule : `FREQ=${frequency}`,
    startsAt: item.timezone === scheduleTimezone ? startsAt : item.startsAt,
  };
}

export function presentScheduleFrequency(rrule: string): string {
  const frequency = scheduleFrequencyValue(rrule);
  return frequencies.find((item) => item.value === frequency)?.label ?? "Personalizada";
}

export function formatScheduleDateTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "Data indisponível";
  return new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "short", timeZone: scheduleTimezone }).format(date);
}

export function instantToLocalDateTime(value: string, timezone = scheduleTimezone): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "";
  const parts = new Intl.DateTimeFormat("en-CA", {
    year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23", timeZone: timezone,
  }).formatToParts(date);
  const part = (type: Intl.DateTimeFormatPartTypes) => parts.find((entry) => entry.type === type)?.value ?? "00";
  return `${part("year")}-${part("month")}-${part("day")}T${part("hour")}:${part("minute")}`;
}

export function validateScheduleSettings(deadlineValue: string, reminderValues: string[]) {
  const deadlineMinutes = Number(deadlineValue);
  if (!Number.isInteger(deadlineMinutes) || deadlineMinutes <= 0) return { error: "Informe um prazo inteiro maior que zero." as const };
  if (reminderValues.length > 3) return { error: "Adicione no máximo três lembretes." as const };
  const reminders: number[] = [];
  for (const value of reminderValues) {
    const offset = Number(value);
    if (value.trim() === "" || !Number.isInteger(offset) || offset < 0 || offset > deadlineMinutes) {
      return { error: "Cada lembrete deve ser um número inteiro entre zero e o prazo." as const };
    }
    if (reminders.includes(offset)) return { error: "Os horários dos lembretes precisam ser diferentes." as const };
    reminders.push(offset);
  }
  return { deadlineMinutes, reminders };
}

function presentReminders(reminders: number[]) {
  if (!reminders.length) return "sem lembretes";
  return reminders.map((offset) => offset === 0 ? "no início" : `${offset} min após o início`).join(", ");
}

function mutationId() { return globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random()}`; }
function formatMutationErrors(errors: Array<{ message: string; code: string }>) {
  const conflict = errors.find((error) => error.code === "CONFLICT" || error.code === "VERSION_CONFLICT");
  return conflict ? `Conflito de versão: ${conflict.message} Recarregue os dados e tente novamente.` : errors.map((error) => error.message).join(" ");
}
function formatFailure(error: unknown) {
  const failure = error as GraphQLFailure;
  return failure.code === "CONFLICT" || failure.code === "VERSION_CONFLICT"
    ? `Conflito de versão: ${failure.message} Recarregue os dados e tente novamente.`
    : failure.message ?? "Não foi possível concluir a operação.";
}
