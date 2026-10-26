import { describe, expect, it, vi } from "vitest";
import {
  buildActivationSummary,
  buildReviewGroups,
  isExpectedVersionConflict,
  inventoryAvailabilityLabel,
  isPreparingDraft,
} from "@/features/dashboard/baselines/review-model";
import { RejectItemForm } from "@/features/dashboard/baselines/review-forms";
import { baselineVersionPresentation, buildCoverageSummary, conditionLabel } from "@/features/dashboard/inventory-presentation";
import { act, createElement } from "react";
import { createRoot } from "react-dom/client";

describe("baseline review view models", () => {
  it("UT-350 groups review items by environment without changing API order", () => {
    const draft = { environments: [{ id: "room", name: "Sala", items: [{ id: "second" }, { id: "first" }] }] } as never;
    expect(buildReviewGroups(draft)[0].items.map((item) => item.id)).toEqual(["second", "first"]);
  });

  it("UT-351 uses the condition vocabulary", () => {
    expect(conditionLabel("NEW")).toBe("Novo — Sem sinais de uso");
    expect(conditionLabel("FAIR")).toBe("Regular — Desgaste ou danos pequenos visíveis que não comprometem o uso");
    expect(conditionLabel("NOT_ASSESSABLE")).toContain("Evidência insuficiente");
  });

  it("renders rejection reason choices and submits a backend reason code", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const host = document.createElement("div");
    const root = createRoot(host);
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    await act(async () => { root.render(createElement(RejectItemForm, { itemName: "Sofá", onSubmit })); });
    const reason = host.querySelector<HTMLSelectElement>("select");
    expect([...reason!.options].map((option) => option.textContent)).toEqual(["Selecione um motivo", "Não existe no imóvel", "Não é relevante para o inventário", "Item duplicado", "Objeto pessoal", "Outro motivo"]);
    await act(async () => { reason!.value = "DUPLICATE"; reason!.dispatchEvent(new Event("change", { bubbles: true })); });
    await act(async () => { host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true })); });
    expect(onSubmit).toHaveBeenCalledWith("DUPLICATE", null);
    await act(async () => { root.unmount(); });
  });

  it("UT-352 summarizes included, rejected and not reviewed items", () => {
    expect(buildActivationSummary([{ decision: "ACCEPTED" }, { decision: "EDITED" }, { decision: "REJECTED" }, { decision: "PENDING" }], 1)).toEqual({ included: 2, rejected: 1, notReviewed: 1 });
  });

  it("UT-353 identifies expectedVersion conflicts and offers a refresh path", () => {
    expect(isExpectedVersionConflict(Object.assign(new Error("Conflito"), { code: "CONFLICT" }))).toBe(true);
    expect(isExpectedVersionConflict(new Error("Falha de rede"))).toBe(false);
  });

  it("UT-356 shows baseline coverage and keeps added environments separate", () => {
    expect(buildCoverageSummary({ expectedItems: 24, assessedItems: 18, environmentsAddedDuringCapture: ["Depósito"] })).toEqual({ label: "18 de 24 itens avaliados", addedEnvironments: ["Depósito"] });
    expect(buildCoverageSummary(null)).toBeNull();
  });

  it("UT-357 presents version, purpose, activation, author, status and all contestation states", () => {
    expect(baselineVersionPresentation({ versionNumber: 3, purpose: "MOVE_IN", activatedAt: "2026-10-20T12:00:00Z", activatedBy: "membership-7", status: "ACTIVE", contestationSummary: { open: 1, acknowledged: 2, contested: 3, noResponse: 4 } }, "Gestora QA")).toEqual({
      title: "Versão 3", purpose: "Entrada de locação", activatedAt: new Intl.DateTimeFormat("pt-BR", { dateStyle: "medium" }).format(new Date("2026-10-20T12:00:00Z")), author: "Autor Gestora QA", status: "Ativa", contestation: "1 abertas · 2 aceitas · 3 contestadas · 4 sem resposta no prazo",
    });
  });

  it("UT-355 and UT-358 labels each source-retention state", () => {
    expect(inventoryAvailabilityLabel("EXPIRED")).toBe("original não disponível; quadro preservado");
    expect(inventoryAvailabilityLabel("ACTIVE_BASELINE")).toContain("inventário estiver ativo");
    expect(inventoryAvailabilityLabel("CONTESTATION_HOLD")).toContain("contestação estiver aberta");
    expect(inventoryAvailabilityLabel("LEGAL_HOLD")).toContain("retenção legal");
    expect(inventoryAvailabilityLabel("AVAILABLE")).toBe("original disponível");
  });

  it("UT-364 exposes a preparing state", () => {
    expect(isPreparingDraft({ version: { draftState: "PREPARING" } })).toBe(true);
    expect(isPreparingDraft({ version: { draftState: "READY" } })).toBe(false);
  });
});
