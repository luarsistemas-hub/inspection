import * as React from "react";
import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { HandoverForms } from "../../app/capture/[linkToken]/descriptive-capture";
import { validateHandoverPayload } from "../../src/pwa/descriptive-checklist";

describe("payloads emitidos pelos formulários de entrega", () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, React });
  it("envia KEY, CONTROL, METER e NOTE com campos aceitos pelo contrato", async () => {
    const host = document.createElement("div"); const root = createRoot(host);
    const emittedKinds: string[] = [];
    const onSave = vi.fn(async (input: { recordKey: string; kind: string; payload: Record<string, unknown>; mediaIds: string[]; declarant: string; expectedVersion: number }) => { emittedKinds.push(input.kind); return "saved" as const; });
    const records = ["KEY", "CONTROL", "METER", "NOTE"].map((kind) => ({ key: kind.toLowerCase(), kind, required: true }));
    await act(async () => { root.render(createElement(HandoverForms, { records, savedRecords: [], onSave })); });
    const setInput = async (form: Element, index: number, value: string) => {
      const field = form.querySelectorAll("input,textarea")[index];
      if (!field) throw new Error(`Campo ausente: ${index}`);
      await act(async () => { const descriptor = Object.getOwnPropertyDescriptor(field instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype, "value"); descriptor?.set?.call(field, value); field.dispatchEvent(new Event("input", { bubbles: true })); });
    };
    const forms = [...host.querySelectorAll("form")];
    await setInput(forms[0], 0, "portão"); await setInput(forms[0], 1, "2");
    await setInput(forms[1], 0, "garagem");
    await setInput(forms[2], 0, "ENERGIA"); await setInput(forms[2], 1, "medidor-01"); await setInput(forms[2], 2, "123.4"); await setInput(forms[2], 3, "kWh"); await setInput(forms[2], 4, "2026-11-03");
    await setInput(forms[3], 0, "Registro geral");
    for (const form of forms) await act(async () => { form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); });
    expect(onSave).toHaveBeenCalledTimes(4);
    expect(emittedKinds).toEqual(["KEY", "CONTROL", "METER", "NOTE"]);
    for (const [input] of onSave.mock.calls) expect(validateHandoverPayload(input.kind, input.payload)).toBeUndefined();
    expect(onSave.mock.calls.map(([input]) => [input.kind, input.payload])).toEqual([
      ["KEY", { keyType: "portão", quantity: 2 }],
      ["CONTROL", { controlType: "garagem" }],
      ["METER", { meterType: "ENERGIA", identifier: "medidor-01", reading: 123.4, unit: "kWh", date: "2026-11-03" }],
      ["NOTE", { note: "Registro geral" }],
    ]);
    await act(async () => { root.unmount(); });
  });
});
