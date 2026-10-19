import { describe, expect, it } from "vitest";
import { canActivateOriginVersion, originVersionStatus, selectableOriginVersions } from "@/features/dashboard/origin-controls";
import type { DashboardOriginVersionsQuery } from "@/graphql/generated";

type OriginVersion = DashboardOriginVersionsQuery["originVersions"]["nodes"][number];

function origin(overrides: Partial<OriginVersion> = {}): OriginVersion {
  return {
    id: "origin-a", originId: "origin-root-a", versionNumber: 1, status: "ACTIVE", mediaKind: "VIDEO",
    preparationStatus: "READY", captureSubmitted: true, selectable: true, sourceAvailability: "RETAINED",
    scopeManifestDigest: "digest", supersedesId: null, activatedAt: "2030-05-10T12:00:00Z", ...overrides,
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

  it("communicates pending, failed, expired and ready states in text", () => {
    expect(originVersionStatus("DRAFT", "RECEIVED", false)).toBe("Origem recebida; preparação pendente");
    expect(originVersionStatus("ACTIVE", "FAILED", false)).toBe("Preparação com falha");
    expect(originVersionStatus("EXPIRED", "READY", false)).toBe("Expirada");
    expect(originVersionStatus("ACTIVE", "READY", true)).toBe("Pronta para seleção");
  });

  it("only offers activation for submitted PHOTO and ready submitted VIDEO drafts", () => {
    expect(canActivateOriginVersion(origin({ status: "DRAFT", mediaKind: "PHOTO", captureSubmitted: false }))).toBe(false);
    expect(canActivateOriginVersion(origin({ status: "DRAFT", mediaKind: "PHOTO", captureSubmitted: true }))).toBe(true);
    expect(canActivateOriginVersion(origin({ status: "DRAFT", mediaKind: "VIDEO", preparationStatus: "RECEIVED", captureSubmitted: true }))).toBe(false);
    expect(canActivateOriginVersion(origin({ status: "DRAFT", mediaKind: "VIDEO", preparationStatus: "READY", captureSubmitted: true }))).toBe(true);
  });
});
