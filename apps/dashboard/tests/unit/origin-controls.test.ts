import { describe, expect, it } from "vitest";
import { originVersionStatus, selectableOriginVersions } from "@/features/dashboard/origin-controls";
import type { DashboardOriginVersionsQuery } from "@/graphql/generated";

type OriginVersion = DashboardOriginVersionsQuery["originVersions"]["nodes"][number];

function origin(overrides: Partial<OriginVersion> = {}): OriginVersion {
  return {
    id: "origin-a", originId: "origin-root-a", versionNumber: 1, status: "ACTIVE", mediaKind: "VIDEO",
    preparationStatus: "READY", captureSubmitted: true, selectable: true, source: null, sourceInspectionId: null,
    draftState: null, sourceAvailability: "RETAINED",
    scopeManifestDigest: "digest", supersedesId: null, activatedAt: "2030-05-10T12:00:00Z", ...overrides,
    purpose: overrides.purpose ?? null, hasInventory: overrides.hasInventory ?? false,
    limitedCoverage: overrides.limitedCoverage ?? false,
  };
}

describe("Dashboard origin controls", () => {
  it("only offers selectable versions matching the template media mode", () => {
    const versions = [origin(), origin({ id: "pending", selectable: false }), origin({ id: "photo", mediaKind: "PHOTO" })];

    expect(selectableOriginVersions(versions, "VIDEO").map(({ id }) => id)).toEqual(["origin-a"]);
    expect(selectableOriginVersions(versions, "PHOTO").map(({ id }) => id)).toEqual(["photo"]);
    expect(selectableOriginVersions(versions, undefined)).toEqual([]);
  });

  it("offers an inventory origin for either capture mode", () => {
    const inventory = origin({ id: "inventory", mediaKind: "PHOTO", hasInventory: true, preparationStatus: null });

    expect(selectableOriginVersions([inventory], "VIDEO").map(({ id }) => id)).toEqual(["inventory"]);
    expect(selectableOriginVersions([inventory], "PHOTO").map(({ id }) => id)).toEqual(["inventory"]);
  });

  it("labels inventory drafts by source and draft state", () => {
    expect(originVersionStatus(origin({ status: "DRAFT", source: "INSPECTION_PROMOTION", draftState: "READY" }))).toBe("Rascunho de promoção pronto para revisão");
    expect(originVersionStatus(origin({ status: "DRAFT", source: "CAPTURE", draftState: "PREPARING" }))).toBe("Rascunho de captura em preparação");
    expect(originVersionStatus(origin({ status: "DRAFT", source: "OTHER", draftState: "FAILED" }))).toBe("Rascunho de inventário com falha");
    expect(originVersionStatus(origin({ status: "DRAFT", source: "OTHER", draftState: "OTHER" }))).toBe("Rascunho de inventário");
  });

  it("labels legacy invitations and received origins", () => {
    expect(originVersionStatus(origin({ status: "DRAFT", preparationStatus: null, draftState: null }))).toBe("Convite pendente");
    expect(originVersionStatus(origin({ status: "INVITED", draftState: null }))).toBe("Convite pendente");
    expect(originVersionStatus(origin({ status: "DRAFT", preparationStatus: "READY", draftState: null }))).toBe("Origem recebida; pronta para ativação");
    expect(originVersionStatus(origin({ status: "DRAFT", preparationStatus: "RECEIVED", draftState: null }))).toBe("Origem recebida; preparação pendente");
  });

  it("prioritizes terminal states, preserves active labels and has a stable fallback", () => {
    expect(originVersionStatus(origin({ status: "EXPIRED" }))).toBe("Expirada");
    expect(originVersionStatus(origin({ status: "CLOSED" }))).toBe("Expirada");
    expect(originVersionStatus(origin({ status: "INVALIDATED" }))).toBe("Invalidada");
    expect(originVersionStatus(origin({ status: "DISCARDED" }))).toBe("Descartada");
    expect(originVersionStatus(origin({ status: "SUPERSEDED" }))).toBe("Versão anterior");
    expect(originVersionStatus(origin({ status: "FAILED" }))).toBe("Preparação com falha");
    expect(originVersionStatus(origin({ status: "ACTIVE", preparationStatus: "FAILED" }))).toBe("Preparação com falha");
    expect(originVersionStatus(origin({ status: "ACTIVE", preparationStatus: "RECEIVED", selectable: false }))).toBe("Preparação pendente");
    expect(originVersionStatus(origin({ status: "ACTIVE", preparationStatus: "READY", selectable: true }))).toBe("Pronta para seleção");
    expect(originVersionStatus(origin({ status: "ACTIVE", preparationStatus: null, selectable: false }))).toBe("Ativa, aguardando preparação");
    expect(originVersionStatus(origin({ status: "UNKNOWN", preparationStatus: null }))).toBe("Situação indisponível");
  });
});
