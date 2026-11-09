import { beforeEach, describe, expect, it } from "vitest";
import { clearDescriptiveQueue, incompleteComponentCount, loadQueuedComponentAnswers, loadQueuedHandoverAnswers, mergeComponentAnswer, queueComponentAnswer, queueHandoverAnswer, removeQueuedComponentAnswer, usesVideoCaptureFlow, friendlyCaptureView, validateComponentAnswer, validateHandoverPayload, validateMeterReading, type ComponentAnswer } from "../../src/pwa/descriptive-checklist";

const answer = (state: ComponentAnswer["state"], version = 1): ComponentAnswer => ({ requirementKey: "env:kitchen", componentKey: "floor", viewKey: "overall", state, reason: "", evidenceIds: [], version });

describe("checklist descritivo e declarações", () => {
  beforeEach(() => {
    const values = new Map<string, string>();
    Object.defineProperty(globalThis, "localStorage", { configurable: true, value: { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => values.set(key, value), removeItem: (key: string) => values.delete(key) } });
  });
  it("UT-095: mantém um componente não observado como pendência", () => {
    expect(incompleteComponentCount(1, [answer("NOT_OBSERVED")])).toBe(1);
    expect(incompleteComponentCount(1, [answer("PARTIAL")])).toBe(1);
  });
  it("UT-096: exige motivo para componente não aplicável", () => {
    expect(validateComponentAnswer("NOT_APPLICABLE", " ")).toBe("Informe o motivo para continuar.");
  });
  it("UT-097: repetição após reconexão reutiliza resposta e versão, sem duplicar linha", () => {
    const first = queueComponentAnswer("responsibility-a", { ...answer("PARTIAL", 2), expectedVersion: 1 });
    const pending = queueComponentAnswer("responsibility-a", { ...answer("NOT_OBSERVED", 3), expectedVersion: 2 });
    expect(loadQueuedComponentAnswers("responsibility-a")).toEqual([expect.objectContaining({ state: "NOT_OBSERVED", version: 3, expectedVersion: 1, clientMutationId: first.clientMutationId })]);
    const saved = mergeComponentAnswer([answer("PARTIAL", 2)], answer("NOT_OBSERVED", 2));
    expect(saved).toEqual([answer("NOT_OBSERVED", 2)]);
    removeQueuedComponentAnswer("responsibility-a", pending);
    expect(loadQueuedComponentAnswers("responsibility-a")).toEqual([]);
  });
  it("UT-098: mantém leitura, unidade e data declaradas para conferência", () => {
    const record = { reading: 123.4, unit: "kWh", date: "2026-11-03" };
    expect(validateMeterReading(String(record.reading))).toBeUndefined();
    expect(validateHandoverPayload("METER", { meterType: "ENERGIA", identifier: "medidor-01", ...record })).toBeUndefined();
    expect(record).toEqual({ reading: 123.4, unit: "kWh", date: "2026-11-03" });
  });
  it("UT-099: rejeita leitura negativa sem alterar a declaração anterior", () => {
    const saved = { reading: 123.4, unit: "kWh", date: "2026-11-03" };
    expect(validateMeterReading("-1")).toBe("Informe uma leitura igual ou maior que zero.");
    expect(validateHandoverPayload("METER", { meterType: "ENERGIA", identifier: "medidor-01", reading: -1, unit: "kWh", date: "2026-11-03" })).toContain("leitura");
    expect(validateHandoverPayload("METER", { meterType: "ENERGIA", identifier: "medidor-01", reading: 3, unit: "kWh", date: "" })).toContain("data");
    expect(validateHandoverPayload("METER", { meterType: "ENERGIA", identifier: "medidor-01", reading: 3, unit: "kWh", date: "2026-02-31" })).toContain("data");
    expect(saved.reading).toBe(123.4);
  });
  it("monta payloads de handover compatíveis com KEY, CONTROL, METER e NOTE do servidor", () => {
    expect(validateHandoverPayload("KEY", { keyType: "portão", quantity: 2 })).toBeUndefined();
    expect(validateHandoverPayload("KEY", { quantity: 2 })).toContain("tipo de chave");
    expect(validateHandoverPayload("CONTROL", { controlType: "garagem" })).toBeUndefined();
    expect(validateHandoverPayload("CONTROL", { controlType: "garagem", quantity: "2" })).toContain("quantidade");
    expect(validateHandoverPayload("NOTE", { note: "Observação geral" })).toBeUndefined();
    expect(validateHandoverPayload("NOTE", { note: " " })).toContain("observação");
  });
  it("preserva base e identificador ao editar declaração offline e limpa fila ao concluir", () => {
    queueHandoverAnswer("responsibility-a", { recordKey: "energia", kind: "METER", payload: { reading: 1 }, mediaIds: [], declarant: "Ana", version: 2, expectedVersion: 1 });
    const revised = queueHandoverAnswer("responsibility-a", { recordKey: "energia", kind: "METER", payload: { reading: 2 }, mediaIds: [], declarant: "Ana", version: 3, expectedVersion: 2 });
    expect(loadQueuedHandoverAnswers("responsibility-a")).toEqual([expect.objectContaining({ payload: { reading: 2 }, expectedVersion: 1, clientMutationId: revised.clientMutationId })]);
    clearDescriptiveQueue("responsibility-a");
    expect(loadQueuedHandoverAnswers("responsibility-a")).toEqual([]);
  });
  it("mapeia general/geral para vista geral em português", () => { expect(friendlyCaptureView("general")).toBe("Vista geral"); expect(friendlyCaptureView("geral")).toBe("Vista geral"); });
  it("PRD D-2 mantém a captura de vídeo fora do fluxo de checklist descritivo", () => {
    expect(usesVideoCaptureFlow("VIDEO")).toBe(true);
    expect(usesVideoCaptureFlow("PHOTO")).toBe(false);
  });
});
