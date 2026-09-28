import { describe, expect, it } from "vitest";
import { buildGovernanceRows } from "@/features/admin/governance-presentation";
import { isHistoryForResource } from "@/features/admin/history";
import { RequestGuard } from "@/features/admin/request-guard";

describe("Admin scoped response guards", () => {
  it("UT-049 discards a membership A response after membership B starts loading", () => {
    const guard = new RequestGuard();
    const membershipA = guard.begin();
    const membershipB = guard.begin();
    expect(guard.isCurrent(membershipA)).toBe(false);
    expect(guard.isCurrent(membershipB)).toBe(true);
  });

  it("IT-064 only presents history for the selected resource", () => {
    expect(isHistoryForResource({ ID: "resource-b" }, "resource-a")).toBe(false);
    expect(isHistoryForResource({ ID: "resource-b" }, "resource-b")).toBe(true);
  });

  it("uses an audit row's target rather than treating its event ID as the resource", () => {
    expect(isHistoryForResource({ ID: "event-1", targetId: "asset-1" }, "event-1")).toBe(false);
    expect(isHistoryForResource({ ID: "event-1", targetId: "asset-1" }, "asset-1")).toBe(true);
  });

  it("IT-057 keeps an empty retention collection separate from loaded deliveries", () => {
    const result = buildGovernanceRows(
      { status: "fulfilled", value: { publicationPolicy: { mode: "MANUAL", version: 3 } } },
      { status: "fulfilled", value: { retentionPolicies: { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } } } },
      { status: "fulfilled", value: { notificationDeliveries: { nodes: [{ id: "delivery-1", status: "FAILED", createdAt: "2026-01-01", updatedAt: "2026-01-01", inspectionId: "inspection-1", invitationId: null, logicalTemplate: "invite", failureCode: "BOUNCE", recipientMasked: "a***@example.test", responsibilityVersion: 2, canCorrectResponsibleEmail: true }], pageInfo: { endCursor: null, hasNextPage: false } } } },
    );
    expect(result.rows).toEqual(expect.arrayContaining([
      expect.objectContaining({ Recurso: "Retenção", Situação: "Nenhuma regra configurada" }),
      expect.objectContaining({ Recurso: "Entrega", ID: "delivery-1", Situação: "FAILED" }),
    ]));
  });

  it("IT-060 reports a delivery failure without hiding available policy and retention results", () => {
    const result = buildGovernanceRows(
      { status: "fulfilled", value: { publicationPolicy: { mode: "AUTOMATIC", version: 4 } } },
      { status: "fulfilled", value: { retentionPolicies: { nodes: [{ id: "retention-1", evidenceDays: 30, operationalDays: 60, securityDays: 90, version: 1 }], pageInfo: { endCursor: null, hasNextPage: false } } } },
      { status: "rejected", reason: new Error("transport failure") },
    );
    expect(result.rows).toEqual(expect.arrayContaining([
      expect.objectContaining({ Recurso: "Retenção", ID: "retention-1" }),
      expect.objectContaining({ Recurso: "Entrega", Situação: "Indisponível" }),
    ]));
    expect(result.rows.some((row) => Object.values(row).includes(4))).toBe(true);
    expect(JSON.stringify(result.rows)).not.toContain("transport failure");
  });
});
