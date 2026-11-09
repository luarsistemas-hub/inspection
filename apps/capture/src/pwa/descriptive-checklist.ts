export type ComponentState = "OBSERVED" | "PARTIAL" | "NOT_OBSERVED" | "NOT_APPLICABLE" | "IMPOSSIBLE";
export type ComponentAnswer = { requirementKey: string; componentKey: string; viewKey: string | null; state: string; reason: string; evidenceIds: string[]; version: number; expectedVersion?: number; clientMutationId?: string };
export type HandoverAnswer = { recordKey: string; kind: string; payload: Record<string, unknown>; mediaIds: string[]; declarant: string; version: number; expectedVersion?: number; clientMutationId?: string };

/** PRD D-2 keeps descriptive checklist UI on photo inspections only. */
export function usesVideoCaptureFlow(mediaKind: string): boolean { return mediaKind === "VIDEO"; }

export function validateComponentAnswer(state: ComponentState | "", reason: string, evidenceCount = 0): string | undefined {
  if (!state) return "Selecione o estado do componente antes de salvar.";
  if ((state === "NOT_APPLICABLE" || state === "IMPOSSIBLE") && !reason.trim()) return "Informe o motivo para continuar.";
  if ((state === "OBSERVED" || state === "PARTIAL") && evidenceCount === 0) return "Registre uma foto antes de marcar o componente como observado.";
  return undefined;
}

export function mergeComponentAnswer(answers: ComponentAnswer[], next: ComponentAnswer): ComponentAnswer[] {
  const existing = answers.find((item) => item.requirementKey === next.requirementKey && item.componentKey === next.componentKey && item.viewKey === next.viewKey);
  if (!existing) return [...answers, next];
  return answers.map((item) => item === existing ? next : item);
}

export function incompleteComponentCount(expected: number, answers: ComponentAnswer[]): number {
  const grouped = new Map<string, Set<string>>();
  for (const item of answers) {
    const key = `${item.requirementKey}:${item.componentKey}`;
    const states = grouped.get(key) ?? new Set<string>(); states.add(item.state); grouped.set(key, states);
  }
  const complete = [...grouped.values()].filter((states) => states.size === 1 && (states.has("OBSERVED") || states.has("NOT_APPLICABLE"))).length;
  return Math.max(0, expected - complete);
}

export function validateMeterReading(reading: string): string | undefined {
  const value = Number(reading.replace(",", "."));
  if (!reading.trim() || !Number.isFinite(value) || value < 0) return "Informe uma leitura igual ou maior que zero.";
  return undefined;
}

export function validateHandoverPayload(kind: string, payload: Record<string, unknown>): string | undefined {
  const allowed: Record<string, string[]> = { KEY: ["keyType", "quantity", "note", "observation"], CONTROL: ["controlType", "quantity", "note", "observation"], METER: ["meterType", "identifier", "reading", "unit", "date", "note", "observation"], NOTE: ["note"] };
  if (!allowed[kind] || Object.keys(payload).some((key) => !allowed[kind].includes(key))) return "Os dados informados não são válidos para esta declaração.";
  const nonempty = (key: string) => typeof payload[key] === "string" && String(payload[key]).trim().length > 0;
  if (kind === "NOTE") return nonempty("note") && String(payload.note).length <= 2000 ? undefined : "Informe uma observação de até 2.000 caracteres.";
  if (kind === "KEY" || kind === "CONTROL") {
    const typeField = kind === "KEY" ? "keyType" : "controlType";
    if (!nonempty(typeField)) return kind === "KEY" ? "Informe o tipo de chave entregue." : "Informe o tipo de controle entregue.";
    if (kind === "KEY" && !("quantity" in payload)) return "Informe a quantidade entregue.";
    if ("quantity" in payload && (typeof payload.quantity !== "number" || !Number.isFinite(payload.quantity) || payload.quantity < 0 || !Number.isInteger(payload.quantity))) return "Informe uma quantidade igual ou maior que zero.";
  } else {
    if (!nonempty("meterType")) return "Informe o tipo do medidor.";
    if (!nonempty("identifier")) return "Informe a identificação do medidor.";
    if (typeof payload.reading !== "number" || !Number.isFinite(payload.reading) || payload.reading < 0) return "Informe uma leitura numérica válida.";
    if (!nonempty("unit")) return "Informe a unidade da leitura.";
    const date = String(payload.date ?? "");
    const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date);
    if (!nonempty("date") || !match) return "Informe uma data válida para a leitura.";
    const parsed = new Date(Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3])));
    if (parsed.toISOString().slice(0, 10) !== date) return "Informe uma data válida para a leitura.";
  }
  for (const key of ["note", "observation"]) if (typeof payload[key] === "string" && Array.from(String(payload[key])).length > 2000) return "A observação excede o limite permitido.";
  return undefined;
}

const queueKey = (responsibilityId: string) => `inspection.component-answers.${responsibilityId}`;
const handoverQueueKey = (responsibilityId: string) => `inspection.handover-records.${responsibilityId}`;
const stableId = () => globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`;
export function queueComponentAnswer(responsibilityId: string, answer: ComponentAnswer): ComponentAnswer {
  const queued = loadQueuedComponentAnswers(responsibilityId);
  const identity = (item: ComponentAnswer) => `${item.requirementKey}:${item.componentKey}:${item.viewKey ?? ""}`;
  const existing = queued.find((item) => identity(item) === identity(answer));
  const updated = queued.filter((item) => identity(item) !== identity(answer));
  const queuedAnswer = { ...answer, expectedVersion: existing?.expectedVersion ?? answer.expectedVersion ?? Math.max(0, answer.version - 1), clientMutationId: existing?.clientMutationId ?? answer.clientMutationId ?? stableId() };
  updated.push(queuedAnswer);
  localStorage.setItem(queueKey(responsibilityId), JSON.stringify(updated));
  return queuedAnswer;
}

export function queueHandoverAnswer(responsibilityId: string, answer: HandoverAnswer): HandoverAnswer {
  const queued = loadQueuedHandoverAnswers(responsibilityId);
  const existing = queued.find((item) => item.recordKey === answer.recordKey);
  const updated = queued.filter((item) => item.recordKey !== answer.recordKey);
  const queuedAnswer = { ...answer, expectedVersion: existing?.expectedVersion ?? answer.expectedVersion ?? Math.max(0, answer.version - 1), clientMutationId: existing?.clientMutationId ?? answer.clientMutationId ?? stableId() };
  updated.push(queuedAnswer);
  localStorage.setItem(handoverQueueKey(responsibilityId), JSON.stringify(updated));
  return queuedAnswer;
}
export function loadQueuedHandoverAnswers(responsibilityId: string): HandoverAnswer[] {
  try { const value: unknown = JSON.parse(localStorage.getItem(handoverQueueKey(responsibilityId)) ?? "[]"); return Array.isArray(value) ? value as HandoverAnswer[] : []; } catch { return []; }
}
export function removeQueuedHandoverAnswer(responsibilityId: string, answer: HandoverAnswer): void {
  const remaining = loadQueuedHandoverAnswers(responsibilityId).filter((item) => item.recordKey !== answer.recordKey);
  if (remaining.length) localStorage.setItem(handoverQueueKey(responsibilityId), JSON.stringify(remaining)); else localStorage.removeItem(handoverQueueKey(responsibilityId));
}
export function clearDescriptiveQueue(responsibilityId: string): void {
  localStorage.removeItem(queueKey(responsibilityId));
  localStorage.removeItem(handoverQueueKey(responsibilityId));
}
export function loadQueuedComponentAnswers(responsibilityId: string): ComponentAnswer[] {
  try { const value: unknown = JSON.parse(localStorage.getItem(queueKey(responsibilityId)) ?? "[]"); return Array.isArray(value) ? value as ComponentAnswer[] : []; } catch { return []; }
}
export function removeQueuedComponentAnswer(responsibilityId: string, answer: ComponentAnswer): void {
  const identity = (item: ComponentAnswer) => `${item.requirementKey}:${item.componentKey}:${item.viewKey ?? ""}`;
  const remaining = loadQueuedComponentAnswers(responsibilityId).filter((item) => identity(item) !== identity(answer));
  if (remaining.length) localStorage.setItem(queueKey(responsibilityId), JSON.stringify(remaining));
  else localStorage.removeItem(queueKey(responsibilityId));
}

export function friendlyCaptureView(view: string): string {
  if (view.toLowerCase() === "general" || view.toLowerCase() === "geral") return "Vista geral";
  return view.split(/[_-]+/).filter(Boolean).map((word) => word[0].toUpperCase() + word.slice(1)).join(" ");
}
