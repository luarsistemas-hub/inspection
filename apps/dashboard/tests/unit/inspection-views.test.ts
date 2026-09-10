import { beforeEach, describe, expect, it } from "vitest";
import {
  formatInspectionDate,
  formatInspectionStatus,
  filterInspections,
  groupInspectionsByStatus,
  inspectionViewStorageKey,
  persistInspectionView,
  readInspectionView,
  sortInspectionsByDueAt,
  type InspectionRecord,
} from "@/features/dashboard/inspection-views";

function inspection(id: string, status: string, dueAt: string): InspectionRecord {
  return { id, assetId: "asset", participantId: "participant", projectId: null, stageId: null, source: "MANUAL", sourceReason: null, stateReason: null, status, evidenceCount: 2, dueAt, deadlineAt: dueAt, reminderInstants: [], version: 1 };
}

describe("Inspection views", () => {
  beforeEach(() => localStorage.clear());

  it("persists and reads the selected view per membership", () => {
    persistInspectionView("membership-a", "quadro");
    persistInspectionView("membership-b", "agenda");

    expect(localStorage.getItem(inspectionViewStorageKey("membership-a"))).toBe("quadro");
    expect(readInspectionView("membership-a")).toBe("quadro");
    expect(readInspectionView("membership-b")).toBe("agenda");
    expect(readInspectionView("membership-c")).toBe("lista");
  });

  it("groups every supported status without losing records", () => {
    const records = [
      inspection("planned", "PLANNED", "2026-09-10T10:00:00Z"),
      inspection("in-progress", "IN_PROGRESS", "2026-09-10T11:00:00Z"),
      inspection("completed", "COMPLETED", "2026-09-10T12:00:00Z"),
      inspection("canceled", "CANCELED", "2026-09-10T13:00:00Z"),
    ];
    const groups = groupInspectionsByStatus(records);

    expect(groups.planejamento.map(({ id }) => id)).toEqual(["planned"]);
    expect(groups.execucao.map(({ id }) => id)).toEqual(["in-progress"]);
    expect(groups.concluidas.map(({ id }) => id)).toEqual(["completed"]);
    expect(groups.encerradas.map(({ id }) => id)).toEqual(["canceled"]);
    expect(Object.values(groups).flat()).toHaveLength(records.length);
  });

  it("sorts the agenda by due date and puts invalid dates last", () => {
    const records = [inspection("late", "INVITED", "2026-09-12T10:00:00Z"), inspection("invalid", "INVITED", "not-a-date"), inspection("early", "INVITED", "2026-09-10T10:00:00Z")];

    expect(sortInspectionsByDueAt(records).map(({ id }) => id)).toEqual(["early", "late", "invalid"]);
    expect(formatInspectionDate(undefined)).toBe("Data não informada");
    expect(formatInspectionDate("not-a-date")).toBe("Data inválida");
  });

  it("keeps an unknown status visible with a legible fallback", () => {
    const groups = groupInspectionsByStatus([inspection("unknown", "FUTURE_STATUS", "2026-09-10T10:00:00Z")]);

    expect(groups.planejamento.map(({ id }) => id)).toEqual(["unknown"]);
    expect(formatInspectionStatus("INVITED")).toBe("Convidada");
    expect(formatInspectionStatus("FUTURE_STATUS")).toBe("Status desconhecido");
    expect(formatInspectionStatus("")).toBe("Status desconhecido");
  });

  it("filters the loaded collection locally by context and text", () => {
    const records = [inspection("planned", "PLANNED", "2026-09-10T10:00:00Z"), inspection("done", "COMPLETED", "2026-09-11T10:00:00Z")];

    expect(filterInspections(records, "done", "todas").map(({ id }) => id)).toEqual(["done"]);
    expect(filterInspections(records, "", "planejamento").map(({ id }) => id)).toEqual(["planned"]);
    expect(filterInspections(records, "missing", "todas")).toEqual([]);
  });
});
