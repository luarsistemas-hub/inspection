import { describe, expect, it } from "vitest";
import { createElement, type ComponentProps } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { CustomerReportVisual, ReportVisual } from "@/features/dashboard/report-visual";
import { inventoryStatusLabel, noBasisLabel } from "@/features/dashboard/inventory-presentation";

const entry = (name: string, status: string) => ({ environment: "Sala", type: "SOFA", name, status, conditionBefore: null, conditionAfter: null, reason: null, pairs: [] });

const inventory = {
  referenceVersionNumber: 1,
  purpose: "MOVE_IN",
  coverage: { expectedItems: 3, assessedItems: 1, notObserved: 1, noBasis: 1, environmentsNotCaptured: [], environmentsAddedDuringCapture: [] },
  problems: [entry("Mesa", "NOT_OBSERVED")],
  informational: [entry("Quadro", "NO_BASIS"), entry("Luminária", "FUTURE_STATUS")],
};

const context = { asset: { id: "asset", name: "Imóvel QA", address: "Rua QA", externalKey: "QA-1" }, participant: { id: "p", name: "QA" }, template: { id: "t", name: "Padrão", version: 1 }, inspection: { stageLabel: null } };

describe("inventory statuses of comparison reports (task_05 round 3, item 4)", () => {
  it("labels NO_BASIS and never presents an unknown status as a change", () => {
    expect(inventoryStatusLabel("NO_BASIS")).toBe("Sem base para comparar");
    expect(inventoryStatusLabel("NOT_OBSERVED")).toBe("Não observado");
    for (const unknown of ["FUTURE_STATUS", "", "no_basis"]) {
      expect(inventoryStatusLabel(unknown)).toBe("Situação não reconhecida");
      expect(inventoryStatusLabel(unknown)).not.toMatch(/alter|dano|encontrad/i);
    }
    expect(noBasisLabel(0)).toBeNull();
    expect(noBasisLabel(1)).toBe("1 item sem base para comparar");
    expect(noBasisLabel(2)).toBe("2 itens sem base para comparar");
  });

  it("shows no-basis items and counts apart from not observed on the internal and customer screens", () => {
    const internal = { id: "report-1", version: 1, classification: "ATTENTION", classificationLabel: "Requer atenção", limited: true, advisory: "Triagem.", context, requirements: [], evidence: { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } }, findings: [], inventory } as unknown as ComponentProps<typeof ReportVisual>["report"];
    const customer = { inspectionId: "i", snapshotId: "s", version: 1, classification: "ATTENTION", classificationLabel: "Requer atenção", limited: true, advisory: "Laudo.", status: "PUBLISHED", historical: false, context, requirements: [], inventory } as unknown as ComponentProps<typeof CustomerReportVisual>["report"];
    const screens = [
      renderToStaticMarkup(createElement(ReportVisual, { report: internal, onDownload: () => undefined })),
      renderToStaticMarkup(createElement(CustomerReportVisual, { report: customer, evidence: [] })),
    ];
    for (const html of screens) {
      expect(html).toContain("Quadro · Sem base para comparar");
      expect(html).toContain("1 item sem base para comparar");
      expect(html).toContain("Luminária · Situação não reconhecida");
      expect(html).not.toContain("Alteração identificada");
    }
  });
});
