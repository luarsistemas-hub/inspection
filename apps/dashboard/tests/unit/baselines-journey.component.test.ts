import { act, createElement, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { graphql } from "@/graphql/client";
import * as G from "@/graphql/generated";
import { BaselinesJourney } from "@/features/dashboard/baselines-journey";

const navigation = vi.hoisted(() => ({ pathname: "/baselines/review/draft-a", push: vi.fn() }));
vi.mock("next/navigation", () => ({ usePathname: () => navigation.pathname, useRouter: () => ({ push: navigation.push }) }));
vi.mock("@/graphql/client", () => ({ graphql: vi.fn() }));
vi.mock("@inspection/design-system", async () => {
  const React = await import("react");
  return {
    Button: ({ children, ...props }: ButtonHTMLAttributes<HTMLButtonElement>) => React.createElement("button", props, children),
    Field: ({ label, children }: { label: string; children: ReactNode }) => React.createElement("label", null, label, children),
    Input: (props: InputHTMLAttributes<HTMLInputElement>) => React.createElement("input", props),
    Select: (props: SelectHTMLAttributes<HTMLSelectElement>) => React.createElement("select", props),
    Textarea: (props: TextareaHTMLAttributes<HTMLTextAreaElement>) => React.createElement("textarea", props),
    Dialog: () => null,
  };
});
vi.mock("@/features/dashboard/form-dialog", () => ({ FormDialog: () => null }));
vi.mock("@/features/dashboard/confirmation-dialog", () => ({ ConfirmationDialog: () => null }));

describe("BaselinesJourney rendered states", () => {
  beforeEach(() => { navigation.pathname = "/baselines/review/draft-a"; navigation.push.mockReset(); });
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

  it("UT-364 displays PREPARING copy and withholds review actions", async () => {
    vi.mocked(graphql).mockImplementation(async (document) => {
      const operation = (document as unknown as { definitions?: readonly { name?: { value?: string } }[] }).definitions?.[0]?.name?.value;
      if (operation === "DashboardBaselineAssets") return { assets: { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } } } as never;
      if (operation === "DashboardCounterpartyOptions") return { participants: { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } } } as never;
      if (operation === "DashboardBaselineVersions") return { baselineVersions: { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } } } as never;
      if (operation === "DashboardInventoryDraft") return { inventoryDraft: { version: { id: "draft-a", originRevision: 1, versionNumber: 2, status: "DRAFT", draftState: "PREPARING", hasInventory: true, purpose: null, counts: { included: 0, rejected: 0, notReviewed: 0 } }, progress: { decided: 0, pending: 0 }, readOnly: false, noticeCode: null, warnings: [], environments: [] } } as never;
      throw new Error(`Unexpected operation ${operation}`);
    });
    const host = document.createElement("div");
    const root = createRoot(host);
    await act(async () => { root.render(createElement(BaselinesJourney, { canReview: true })); await new Promise((resolve) => setTimeout(resolve, 0)); });
    expect(host.textContent).toContain("O inventário está sendo preparado");
    expect(host.textContent).not.toContain("Aceitar");
    expect(host.textContent).not.toContain("Ativar inventário");
    await act(async () => { root.unmount(); });
  });

  it("UT-350 renders review items in the order returned by the API", async () => {
    vi.mocked(graphql).mockImplementation(async (document) => {
      const operation = (document as unknown as { definitions?: readonly { name?: { value?: string } }[] }).definitions?.[0]?.name?.value;
      if (operation === "DashboardBaselineAssets") return { assets: { nodes: [{ id: "asset-a", name: "Imóvel QA", externalKey: "QA" }], pageInfo: { endCursor: null, hasNextPage: false } } } as never;
      if (operation === "DashboardCounterpartyOptions") return { participants: { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } } } as never;
      if (operation === "DashboardBaselineVersions") return { baselineVersions: { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } } } as never;
      if (operation === "DashboardInventoryDraft") return { inventoryDraft: { assetId: "asset-a", version: { id: "draft-a", originRevision: 1, versionNumber: 2, status: "DRAFT", draftState: "READY", hasInventory: true, purpose: null, counts: { included: 0, rejected: 0, notReviewed: 2 } }, progress: { decided: 0, pending: 2 }, readOnly: false, noticeCode: null, warnings: [], environments: [{ id: "environment-a", identityId: "environment-identity-a", typeCode: "LIVING_ROOM", name: "Sala", expected: true, addedDuringCapture: false, decision: "PENDING", position: 0, items: [{ id: "item-b", identityId: "identity-b", environmentId: "environment-a", typeCode: "TABLE", name: "Segundo pela API", material: null, color: null, quantity: null, condition: "GOOD", observation: "OBSERVED", limitation: null, confidenceBps: 2000, origin: "AI", declared: false, decision: "PENDING", rejectionReason: null, version: 1, evidence: [] }, { id: "item-a", identityId: "identity-a", environmentId: "environment-a", typeCode: "SOFA", name: "Primeiro pela API", material: null, color: null, quantity: null, condition: "GOOD", observation: "OBSERVED", limitation: null, confidenceBps: 9900, origin: "AI", declared: false, decision: "PENDING", rejectionReason: null, version: 1, evidence: [] }] }] } } as never;
      throw new Error(`Unexpected operation ${operation}`);
    });
    const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => { root.render(createElement(BaselinesJourney, { canReview: true })); await new Promise((resolve) => setTimeout(resolve, 0)); });
    expect([...host.querySelectorAll(".baseline-room-item span")].map((name) => name.textContent)).toEqual(["Segundo pela API", "Primeiro pela API"]);
    expect(host.textContent).toContain("0 incluídos · 0 rejeitados · 2 não revisados");
    await act(async () => { root.unmount(); });
  });

  it("UT-357 renders version metadata and contestation summary from history", async () => {
    navigation.pathname = "/baselines";
    vi.mocked(graphql).mockImplementation(async (document) => {
      const operation = (document as unknown as { definitions?: readonly { name?: { value?: string } }[] }).definitions?.[0]?.name?.value;
      if (operation === "DashboardBaselineAssets") return { assets: { nodes: [{ id: "asset-a", name: "Imóvel QA", externalKey: "QA" }], pageInfo: { endCursor: null, hasNextPage: false } } } as never;
      if (operation === "DashboardReviewers") return { memberships: { nodes: [{ id: "membership-7", tenantId: "tenant-a", name: "Gestora QA", email: "gestora@example.test", role: "MANAGER", status: "ACTIVE", scopes: [] }], pageInfo: { endCursor: null, hasNextPage: false } } } as never;
      if (operation === "DashboardCounterpartyOptions") return { participants: { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } } } as never;
      if (operation === "DashboardBaselineVersions") return { baselineVersions: { nodes: [{ id: "draft-a", originId: "origin-a", versionNumber: 4, status: "DRAFT", mediaKind: "PHOTO", activatedAt: null, activatedBy: null, purpose: null, source: "CAPTURE", sourceInspectionId: null, hasInventory: true, draftState: "READY", sourceAvailability: "AVAILABLE", counts: { included: 0, rejected: 0, notReviewed: 0 }, contestationSummary: { pending: 0, acknowledged: 0, contested: 0, open: 0, noResponse: 0 }, counterpartyInvitations: [] }, { id: "version-a", originId: "origin-a", versionNumber: 3, status: "ACTIVE", mediaKind: "PHOTO", activatedAt: "2026-10-20T12:00:00Z", activatedBy: "membership-7", purpose: "MOVE_IN", source: "CAPTURE", sourceInspectionId: null, hasInventory: true, draftState: "READY", sourceAvailability: "EXPIRED", counts: { included: 18, rejected: 2, notReviewed: 0 }, contestationSummary: { pending: 0, acknowledged: 1, contested: 2, open: 0, noResponse: 1 }, counterpartyInvitations: [] }], pageInfo: { endCursor: null, hasNextPage: false } } } as never;
      if (operation === "Inspections") return { inspections: { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } } } as never;
      throw new Error(`Unexpected operation ${operation}`);
    });
    const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => { root.render(createElement(BaselinesJourney, { canReview: true })); await new Promise((resolve) => setTimeout(resolve, 0)); });
    const history = host.querySelector(".baseline-version-list");
    expect(history?.textContent).toContain("Versão 3 · Entrada de locação");
    expect(history?.textContent).toContain("Autor Gestora QA");
    expect(history?.textContent).toContain("Ativa");
    expect(history?.textContent).toContain("original não disponível; quadro preservado");
    expect(history?.textContent).toContain("0 abertas · 1 aceitas · 2 contestadas · 1 sem resposta no prazo");
    await act(async () => { root.unmount(); });
    navigation.pathname = "/baselines/review/draft-a";
  });

  it("UT-353 renders Atualizar after a version conflict and reloads the real draft", async () => {
    let draftReads = 0;
    vi.mocked(graphql).mockImplementation(async (document) => {
      const operation = (document as unknown as { definitions?: readonly { name?: { value?: string } }[] }).definitions?.[0]?.name?.value;
      if (operation === "DashboardBaselineAssets") return { assets: { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } } } as never;
      if (operation === "DashboardBaselineAsset") return { asset: { id: "asset-a", name: "Imóvel QA", externalKey: "QA-1", businessUnitId: "unit-a" } } as never;
      if (operation === "DashboardCounterpartyOptions") return { participants: { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } } } as never;
      if (operation === "DashboardBaselineVersions") return { baselineVersions: { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } } } as never;
      if (operation === "DashboardInventoryDraft") {
        draftReads += 1;
        const accepted = draftReads > 1;
        return { inventoryDraft: { assetId: "asset-a", version: { id: "draft-a", originRevision: accepted ? 2 : 1, versionNumber: 2, status: "DRAFT", draftState: "READY", hasInventory: true, purpose: null, counts: { included: accepted ? 1 : 0, rejected: 0, notReviewed: accepted ? 0 : 1 } }, progress: { decided: accepted ? 1 : 0, pending: accepted ? 0 : 1 }, readOnly: false, noticeCode: null, warnings: [], environments: [{ id: "environment-a", identityId: "identity-a", typeCode: "LIVING_ROOM", name: "Sala", expected: true, addedDuringCapture: false, decision: "PENDING", position: 0, items: [{ id: "item-a", identityId: "item-identity-a", environmentId: "environment-a", typeCode: "SOFA", name: "Sofá", material: null, color: null, quantity: null, condition: "GOOD", observation: "OBSERVED", limitation: null, confidenceBps: 9500, origin: "CAPTURED", declared: false, decision: accepted ? "ACCEPTED" : "PENDING", rejectionReason: null, version: accepted ? 2 : 1, evidence: [] }] }] } } as never;
      }
      if (operation === "DecideInventoryItems") throw Object.assign(new Error("updated elsewhere"), { code: "CONFLICT" });
      throw new Error(`Unexpected operation ${operation}`);
    });
    const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => { root.render(createElement(BaselinesJourney, { canReview: true })); await new Promise((resolve) => setTimeout(resolve, 0)); });
    await act(async () => { [...host.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.getAttribute("aria-label") === "Aceitar Sofá")?.click(); await new Promise((resolve) => setTimeout(resolve, 0)); });
    expect(host.querySelector('[role="alert"]')?.textContent).toContain("Atualize antes de tentar novamente");
    const update = [...host.querySelectorAll("button")].find((button) => button.textContent === "Atualizar");
    expect(update).toBeTruthy();
    await act(async () => { update?.click(); await new Promise((resolve) => setTimeout(resolve, 0)); });
    expect(draftReads).toBe(2);
    expect([...host.querySelectorAll<HTMLButtonElement>("button")].find((button) => button.getAttribute("aria-label") === "Aceitar Sofá")?.disabled).toBe(true);
    await act(async () => { root.unmount(); });
  });

  it("UT-359 renders a customer baseline without review actions", async () => {
    navigation.pathname = "/baselines";
    vi.mocked(graphql).mockImplementation(async (document) => {
      const operation = (document as unknown as { definitions?: readonly { name?: { value?: string } }[] }).definitions?.[0]?.name?.value;
      if (operation === "DashboardCustomerBaselineAssets") return { customerPortfolio: { nodes: [{ assetId: "asset-a", assetName: "Imóvel QA", assetExternalKey: "QA", status: "ACTIVE", progress: 100, updatedAt: "2026-10-20T12:00:00Z" }], pageInfo: { endCursor: null, hasNextPage: false } } } as never;
      if (operation === "DashboardBaselineInventory") return { baselineInventory: { hasInventory: true, version: { id: "version-a", versionNumber: 2, status: "ACTIVE", purpose: "MOVE_IN", activatedAt: "2026-10-20T12:00:00Z", sourceAvailability: "AVAILABLE" }, environments: [{ id: "environment-a", typeCode: "LIVING_ROOM", name: "Sala", expected: true, addedDuringCapture: false, items: [{ id: "item-a", typeCode: "SOFA", name: "Sofá", material: null, color: null, quantity: null, condition: "GOOD", observation: "OBSERVED", limitation: null, declared: false, decision: "ACCEPTED", evidence: [{ id: "evidence-a", kind: "VIDEO_FRAME", mediaId: "media-a", frameId: "frame-a", timestampMs: 5000, url: null, availability: "AVAILABLE", retentionAvailability: "EXPIRED", position: 0 }] }] }] } } as never;
      throw new Error(`Unexpected operation ${operation}`);
    });
    const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => { root.render(createElement(BaselinesJourney, { canReview: false, isCustomer: true, readHistory: false })); await new Promise((resolve) => setTimeout(resolve, 0)); });
    expect(host.textContent).toContain("Imóvel QA");
    expect(host.textContent).toContain("Sofá");
    expect(host.textContent).not.toContain("Catalogar origem existente");
    expect(host.textContent).not.toContain("Ativar inventário");
    await act(async () => { root.unmount(); });
    navigation.pathname = "/baselines/review/draft-a";
  });
});
