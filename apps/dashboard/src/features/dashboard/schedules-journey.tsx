"use client";

import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "next/navigation";
import { Combobox, type ComboboxOption } from "@inspection/design-system";
import { FormDialog } from "./form-dialog";
import {
  CancelScheduleDocument,
  CreateScheduleDocument,
  SchedulesDocument,
  UpdateScheduleDocument,
  type DashboardFormOptionsQuery,
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

export function SchedulesJourney({
  canMutate,
  options,
}: {
  canMutate: boolean;
  options: { data?: DashboardFormOptionsQuery; loading: boolean; error?: string };
}) {
  const [data, setData] = useState<SchedulesQuery>();
  const [message, setMessage] = useState("Carregando agendas…");
  const [createOpen, setCreateOpen] = useState(false);
  const [editItem, setEditItem] = useState<Schedule>();
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState(false);
  const [createError, setCreateError] = useState("");
  const [editError, setEditError] = useState("");
  const busyRef = useRef(false);
  const openedScheduleRef = useRef<string | undefined>(undefined);
  const scheduleId = useSearchParams().get("scheduleId");

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

  useEffect(() => { void load(scheduleId ?? undefined); }, [load, scheduleId]);
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
      await load();
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
    if (!window.confirm("Cancelar esta agenda? As vistorias históricas serão preservadas.")) return;
    await mutate(CancelScheduleDocument, {
      scheduleId: item.id,
      expectedVersion: item.version,
      clientMutationId: mutationId(),
    });
  };

  return <div className="feature schedules-journey">
    <section className="schedules-list" aria-labelledby="schedules-list-title">
      <div className="schedules-list-heading">
        <h2 id="schedules-list-title">Agendas cadastradas</h2>
        <div className="actions">
          {canMutate && <button onClick={() => { setCreateError(""); setCreateOpen(true); }}>Nova agenda</button>}
          <button className="secondary" onClick={() => void load()}>Atualizar agendas</button>
        </div>
      </div>
      <p role="status" aria-live="polite">{message}</p>
      {data && data.schedules.nodes.length === 0 && <p className="schedules-empty">Crie uma agenda para programar vistorias recorrentes.</p>}
      {data && data.schedules.nodes.length > 0 && <ul className="collection schedules-collection">
        {data.schedules.nodes.map((item) => <li key={item.id}>
          <div className="schedule-summary">
            <strong>{presentDashboardStatus(item.status)}</strong>
            <span>Próxima vistoria: {formatScheduleDateTime(item.nextDueAt)}</span>
            <span>Repetição: {presentScheduleFrequency(item.rrule)}</span>
            <span>Prazo: {item.deadlineMinutes} minutos · {presentReminders(item.reminderOffsetsMinutes)}</span>
          </div>
          {canMutate && item.status === "ACTIVE" && <div className="actions">
            <button className="secondary" onClick={() => { setEditError(""); setEditItem(item); }}>Editar agenda</button>
            <button className="secondary" onClick={() => void cancel(item)}>Cancelar agenda</button>
          </div>}
        </li>)}
      </ul>}
    </section>
    {canMutate && <>
      <FormDialog isOpen={createOpen} onClose={() => setCreateOpen(false)} title="Nova agenda" busy={creating} error={createError}>
        {options.loading && <p role="status">Carregando imóveis, responsáveis pela vistoria e modelos de vistoria…</p>}
        {options.error && <p className="warning" role="alert">Não foi possível carregar as opções do cadastro. {options.error}</p>}
        {options.data && <CreateScheduleForm options={options.data} onSubmit={(input) => void create(input)} busy={creating} />}
      </FormDialog>
      <FormDialog isOpen={Boolean(editItem)} onClose={() => setEditItem(undefined)} title="Editar agenda" busy={editing} error={editError}>
        {editItem && <EditScheduleForm key={`${editItem.id}:${editItem.version}`} item={editItem} onSubmit={(input) => void update(editItem, input)} busy={editing} />}
      </FormDialog>
    </>}
  </div>;
}

function FormOptionsNotice({ options }: { options: { loading: boolean; error?: string } }) {
  if (options.loading) return <p role="status">Carregando imóveis, responsáveis pela vistoria e modelos de vistoria…</p>;
  if (options.error) return <p className="warning" role="alert">Não foi possível carregar as opções do cadastro. {options.error}</p>;
  return null;
}

function CreateScheduleForm({ options, onSubmit, busy }: { options: DashboardFormOptionsQuery; onSubmit: (input: Record<string, unknown>) => void; busy: boolean }) {
  const [assetId, setAssetId] = useState("");
  const [participantId, setParticipantId] = useState("");
  const [templateId, setTemplateId] = useState("");
  const [frequency, setFrequency] = useState("MONTHLY");
  const [startsAt, setStartsAt] = useState("");
  const [deadlineMinutes, setDeadlineMinutes] = useState("60");
  const [reminders, setReminders] = useState(["30"]);
  const [validationError, setValidationError] = useState("");
  const participants = scheduleEntityOptions(options, "participant", assetId);
  const templates = scheduleEntityOptions(options, "template", assetId);

  useEffect(() => {
    if (participantId && !participants.some((option) => option.value === participantId)) setParticipantId("");
    if (templateId && !templates.some((option) => option.value === templateId)) setTemplateId("");
  }, [assetId, participantId, participants, templateId, templates]);

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const settings = validateScheduleSettings(deadlineMinutes, reminders);
    if (settings.error) { setValidationError(settings.error); return; }
    setValidationError("");
    onSubmit({
      assetId,
      participantId,
      templateId,
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
      <RelationshipField label="Imóvel" value={assetId} onChange={(value) => { setAssetId(value); setParticipantId(""); setTemplateId(""); }} options={scheduleEntityOptions(options, "asset")} required />
      <RelationshipField label="Responsável pela vistoria" value={participantId} onChange={setParticipantId} options={participants} required disabled={!assetId} />
      <RelationshipField label="Modelo de vistoria" value={templateId} onChange={setTemplateId} options={templates} required disabled={!assetId} />
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

function RelationshipField({ label, value, onChange, options, required = false, disabled = false }: { label: string; value: string; onChange: (value: string) => void; options: ComboboxOption[]; required?: boolean; disabled?: boolean }) {
  return <label>{label}<Combobox value={value} options={options} onChange={onChange} required={required} disabled={disabled} aria-label={label} /></label>;
}

function scheduleEntityOptions(options: DashboardFormOptionsQuery, kind: "asset" | "participant" | "template", assetId?: string): ComboboxOption[] {
  if (kind === "asset") return options.assets.nodes.filter((item) => item.status === "ACTIVE").map((item) => ({ value: item.id, label: item.name, description: item.externalKey }));
  if (kind === "participant") {
    const asset = options.assets.nodes.find((item) => item.id === assetId);
    const assigned = new Set(asset?.assignments.filter((assignment) => assignment.active).map((assignment) => assignment.participantId));
    return options.participants.nodes.filter((item) => item.status === "ACTIVE" && (!asset || assigned.has(item.id))).map((item) => ({ value: item.id, label: item.name }));
  }
  const asset = options.assets.nodes.find((item) => item.id === assetId);
  return options.templates.nodes.filter((item) => item.activeVersionId && (!asset || item.segmentVersionId === asset.segmentVersionId)).map((item) => ({ value: item.id, label: item.name, description: item.key }));
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
