import * as React from "react";
import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { CaptureReviewCoverage, ComponentChecklist, HandoverForms, HandoverReview } from "../../app/capture/[linkToken]/descriptive-capture";

describe("formulários descritivos do Capture", () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, React });

  it("UT-096 mantém o componente na etapa e pede motivo para não aplicável", async () => {
    const host = document.createElement("div"); const root = createRoot(host);
    const onSave = vi.fn(async () => "saved" as const);
    await act(async () => { root.render(createElement(ComponentChecklist, { rooms: [{ key: "kitchen", name: "Cozinha", components: [{ key: "floor", label: "Piso", views: ["geral"] }] }], answers: [], mediaByRequirement: {}, onSave })); });
    const select = host.querySelector("select");
    await act(async () => { if (select) { select.value = "NOT_APPLICABLE"; select.dispatchEvent(new Event("change", { bubbles: true })); } });
    await act(async () => { host.querySelector("button")?.click(); });
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("Informe o motivo");
    expect(host.textContent).toContain("Cozinha");
    expect(onSave).not.toHaveBeenCalled();
    await act(async () => { root.unmount(); });
  });

  it("UT-095 revisa a cobertura na interface e mantém a lacuna de componente não observado", async () => {
    const host = document.createElement("div"); const root = createRoot(host);
    const onSave = vi.fn(async () => "saved" as const);
    await act(async () => { root.render(createElement(ComponentChecklist, { rooms: [{ key: "kitchen", name: "Cozinha", components: [{ key: "floor", label: "Piso", views: [] }] }], answers: [], mediaByRequirement: {}, onSave })); });
    const select = host.querySelector("select");
    await act(async () => { if (select) { select.value = "NOT_OBSERVED"; select.dispatchEvent(new Event("change", { bubbles: true })); } });
    await act(async () => { host.querySelector("button")?.click(); });
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ state: "NOT_OBSERVED", componentKey: "floor" }));
    await act(async () => { root.render(createElement(CaptureReviewCoverage, { coverage: { expected: 1, fullyAssessed: 0, unknownScope: false, suggestions: [{ requirementKey: "env:kitchen", roomName: "Cozinha", componentKey: "floor", componentLabel: "Piso", missingView: "general" }] } })); });
    expect(host.textContent).toContain("0 de 1 componentes totalmente avaliados.");
    expect(host.textContent).toContain("Cozinha · Piso: fotografe vista geral.");
    await act(async () => { root.unmount(); });
  });

  it("UT-098 emite o payload real do medidor e mostra leitura, unidade e data na revisão", async () => {
    const host = document.createElement("div"); const root = createRoot(host);
    const onSave = vi.fn(async () => "saved" as const);
    await act(async () => { root.render(createElement(HandoverForms, { records: [{ key: "energia", kind: "METER", required: true }], savedRecords: [], onSave })); });
    const fill = async (index: number, value: string) => { const input = host.querySelectorAll<HTMLInputElement>("input")[index]; if (!input) throw new Error(`Campo ausente: ${index}`); await act(async () => { const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set; setter?.call(input, value); input.dispatchEvent(new Event("input", { bubbles: true })); }); };
    await fill(0, "ENERGIA"); await fill(1, "m-01"); await fill(2, "123.4"); await fill(3, "kWh"); await fill(4, "2026-11-03");
    await act(async () => { host.querySelector("form")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); });
    expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ kind: "METER", payload: { meterType: "ENERGIA", identifier: "m-01", reading: 123.4, unit: "kWh", date: "2026-11-03" } }));
    await act(async () => { root.render(createElement(HandoverReview, { records: [{ recordKey: "energia", kind: "METER", payload: { reading: 123.4, unit: "kWh", date: "2026-11-03" }, mediaIds: [], declarant: "Ana", version: 1 }] })); });
    expect(host.textContent).toContain("Leitura: 123.4 kWh · 2026-11-03");
    await act(async () => { root.unmount(); });
  });

  it("UT-099 mantém visível a leitura salva quando a nova leitura é rejeitada", async () => {
    const host = document.createElement("div"); const root = createRoot(host);
    const onSave = vi.fn(async () => "saved" as const);
    await act(async () => { root.render(createElement(HandoverForms, { records: [{ key: "energia", kind: "METER", required: true }], savedRecords: [{ recordKey: "energia", kind: "METER", payload: { meterType: "ENERGIA", identifier: "m-01", reading: 123.4, unit: "kWh", date: "2026-11-03" }, mediaIds: [], declarant: "Ana", version: 1 }], onSave })); });
    const reading = [...host.querySelectorAll("input")].find((input) => input.getAttribute("inputmode") === "decimal");
    expect(host.textContent).toContain("123.4 kWh · 2026-11-03");
    await act(async () => { if (reading) { const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set; setter?.call(reading, "-1"); reading.dispatchEvent(new Event("input", { bubbles: true })); } host.querySelector("form")?.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); });
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("leitura");
    expect(host.textContent).toContain("123.4 kWh · 2026-11-03");
    expect(onSave).not.toHaveBeenCalled();
    await act(async () => { root.unmount(); });
  });
});
