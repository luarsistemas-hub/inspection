import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { graphql } from "@/graphql/client";
import { InventoryTrailView } from "@/features/dashboard/baselines/inventory-trail";
import { sortTrail, trailActionLabel, trailAuthor, trailDetails, type TrailEntry } from "@/features/dashboard/baselines/trail-model";

vi.mock("@/graphql/client", () => ({ graphql: vi.fn() }));

const entry = (overrides: Partial<TrailEntry>): TrailEntry => ({ id: "entry", targetType: "ITEM", targetId: "item-a", action: "ACCEPT", before: null, after: null, reason: null, statement: null, resolutionReason: null, actorMembershipId: null, occurredAt: "2026-10-20T12:00:00Z", ...overrides });

describe("inventory trail model", () => {
  it("labels review, activation and counterparty events", () => {
    expect(trailActionLabel(entry({ action: "AI_SUGGESTED" }))).toBe("Sugestão da IA");
    expect(trailActionLabel(entry({ action: "ADD", targetType: "ENVIRONMENT" }))).toBe("Ambiente adicionado");
    expect(trailActionLabel(entry({ action: "ACTIVATE", targetType: "VERSION" }))).toBe("Versão ativada");
    expect(trailActionLabel(entry({ action: "RESOLVED", targetType: "COUNTERPARTY" }))).toBe("Contestação resolvida");
  });

  it("summarizes condition changes, rejection reasons and contestation outcomes", () => {
    expect(trailDetails(entry({ action: "EDIT", before: { Name: "Pia", Condition: "GOOD" }, after: { Name: "Pia", Condition: "FAIR" }, reason: "Mancha visível" }))).toEqual(["Item: Pia", expect.stringContaining("Condição: Bom"), "Justificativa: Mancha visível"]);
    expect(trailDetails(entry({ action: "REJECT", after: { Name: "Vaso", RejectionReason: "PERSONAL_OBJECT" } }))).toContain("Motivo da rejeição: objeto pessoal");
    expect(trailDetails(entry({ action: "RESOLVED", targetType: "COUNTERPARTY", after: { resolution: "DECLINED" }, resolutionReason: "Fotos confirmam o estado" }))).toEqual(["Contestação não acolhida", "Motivo da resolução: Fotos confirmam o estado"]);
    expect(trailDetails(entry({ action: "AI_SUGGESTED", after: { name: "Sofá", condition: "GOOD", evidenceIds: ["e1", "e2"] } }))).toEqual(["Item: Sofá", expect.stringContaining("Condição sugerida: Bom"), "2 evidência(s) citada(s)"]);
  });

  it("names the author and orders entries chronologically", () => {
    const names = new Map([["member-a", "Ana Gestora"]]);
    expect(trailAuthor(entry({ actorMembershipId: "member-a" }), names)).toBe("Ana Gestora");
    expect(trailAuthor(entry({ actorMembershipId: "member-b" }), names)).toBe("Membro da equipe");
    expect(trailAuthor(entry({ action: "AI_SUGGESTED" }), names)).toBe("Análise automática");
    expect(trailAuthor(entry({ action: "RESPONDED", targetType: "COUNTERPARTY" }), names)).toBe("Contraparte");
    expect(sortTrail([entry({ id: "b", occurredAt: "2026-10-21T00:00:00Z" }), entry({ id: "a", occurredAt: "2026-10-20T00:00:00Z" })]).map((row) => row.id)).toEqual(["a", "b"]);
  });
});

describe("InventoryTrailView", () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

  it("renders a read-only trail with review decisions and the contestation resolution", async () => {
    vi.mocked(graphql).mockImplementation(async (document) => {
      const operation = (document as unknown as { definitions?: readonly { name?: { value?: string } }[] }).definitions?.[0]?.name?.value;
      if (operation === "DashboardInventoryTrail") return { inventoryTrail: {
        suggestions: [entry({ id: "s1", action: "AI_SUGGESTED", after: { name: "Pia", condition: "GOOD", evidenceIds: ["e1"] }, occurredAt: "2026-10-19T12:00:00Z" })],
        decisions: [entry({ id: "d1", action: "EDIT", before: { Name: "Pia", Condition: "GOOD" }, after: { Name: "Pia", Condition: "FAIR" }, reason: "Mancha visível", actorMembershipId: "member-a" })],
        counterpartyEvents: [entry({ id: "c1", targetType: "COUNTERPARTY", action: "RESOLVED", after: { resolution: "ACCEPTED" }, actorMembershipId: "member-a", occurredAt: "2026-10-22T12:00:00Z" })],
      } } as never;
      if (operation === "DashboardReviewers") return { memberships: { nodes: [{ id: "member-a", name: "Ana Gestora" }], pageInfo: { endCursor: null, hasNextPage: false } } } as never;
      throw new Error(`Unexpected operation ${operation}`);
    });
    const host = document.createElement("div");
    const root = createRoot(host);
    await act(async () => { root.render(createElement(InventoryTrailView, { versionId: "version-a" })); await new Promise((resolve) => setTimeout(resolve, 0)); });
    expect(host.textContent).toContain("Trilha de auditoria");
    expect(host.textContent).toContain("Item editado · Ana Gestora");
    expect(host.textContent).toContain("Justificativa: Mancha visível");
    expect(host.textContent).toContain("Contestação acolhida");
    expect(host.textContent).toContain("Sugestão da IA · Análise automática");
    expect(host.querySelectorAll("button, input, select, textarea")).toHaveLength(0);
    await act(async () => { root.unmount(); });
  });
});
