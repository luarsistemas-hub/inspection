import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { createElement, type ComponentProps } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { CustomerReportVisual, ReportVisual } from "@/features/dashboard/report-visual";
import { comparisonOutcomeLabel, inventoryEntryStatusLabel, inventoryStatusLabel } from "@/features/dashboard/inventory-presentation";

// The Go vocabulary test (TestTask05R4OutcomeLabelsAgreeAcrossSurfaces) keeps
// this golden file equal to reportcore.ComparisonOutcomeLabel, the labels of
// the v3 HTML, PDF and GraphQL statusLabel.
const golden = JSON.parse(readFileSync(resolve(__dirname, "../../../../services/inspection/internal/features/reports/core/testdata/comparison-outcome-labels.json"), "utf8")) as Record<string, string>;

const context = { asset: { id: "asset", name: "Imóvel QA", address: "Rua QA", externalKey: "QA-1" }, participant: { id: "p", name: "QA" }, template: { id: "t", name: "Padrão", version: 1 }, inspection: { stageLabel: null } };

function inventoryOf(entries: Array<{ name: string; status: string; statusLabel: string | null }>) {
  const entry = ({ name, status, statusLabel }: { name: string; status: string; statusLabel: string | null }) => ({ environment: "Sala", type: "SOFA", name, status, statusLabel, conditionBefore: null, conditionAfter: null, reason: null, pairs: [] });
  return { referenceVersionNumber: 1, purpose: "MOVE_IN", coverage: null, problems: entries.map(entry), informational: [] };
}

function screens(inventory: ReturnType<typeof inventoryOf>) {
  const internal = { id: "report-1", version: 1, classification: "ATTENTION", classificationLabel: "Requer atenção", limited: true, advisory: "Triagem.", context, requirements: [], evidence: { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } }, findings: [], inventory } as unknown as ComponentProps<typeof ReportVisual>["report"];
  const customer = { inspectionId: "i", snapshotId: "s", version: 1, classification: "ATTENTION", classificationLabel: "Requer atenção", limited: true, advisory: "Laudo.", status: "PUBLISHED", historical: false, context, requirements: [], inventory } as unknown as ComponentProps<typeof CustomerReportVisual>["report"];
  return [
    renderToStaticMarkup(createElement(ReportVisual, { report: internal, onDownload: () => undefined })),
    renderToStaticMarkup(createElement(CustomerReportVisual, { report: customer, evidence: [] })),
  ];
}

describe("one outcome vocabulary per v3 report (task_05 round 4, item 4)", () => {
  it("shows the server label of the six v3 outcomes on the internal and customer screens", () => {
    expect(Object.keys(golden).sort()).toEqual(["CONDITION_CHANGED", "IMPROVED", "MISSING", "NOT_OBSERVED", "NO_BASIS", "PRESENT_UNCHANGED"]);
    for (const [status, label] of Object.entries(golden)) {
      expect(inventoryEntryStatusLabel({ status, statusLabel: label })).toBe(label);
      for (const html of screens(inventoryOf([{ name: `Item ${status}`, status, statusLabel: label }]))) {
        expect(html).toContain(label);
      }
    }
    // "Ausência comprovada", never the v1/v2 wording, in a v3 report.
    for (const html of screens(inventoryOf([{ name: "Micro-ondas", status: "MISSING", statusLabel: golden.MISSING }]))) {
      expect(html).toContain("Ausência comprovada");
      expect(html).not.toContain("Não encontrado");
    }
  });


  it("keeps comparison item labels aligned with the backend golden vocabulary", () => {
    const outcomeToGolden = { UNCHANGED: "PRESENT_UNCHANGED", CHANGED: "CONDITION_CHANGED", IMPROVED: "IMPROVED", MISSING: "MISSING", NOT_OBSERVED: "NOT_OBSERVED", NO_BASIS: "NO_BASIS" } as const;
    for (const [outcome, key] of Object.entries(outcomeToGolden)) {
      expect(comparisonOutcomeLabel(outcome)).toBe(golden[key]);
    }
  });

  it("keeps the historical v1/v2 labels when the entry has no server label", () => {
    const legacy: Record<string, string> = { MISSING: "Não encontrado", CONDITION_CHANGED: "Condição alterada", PRESENT_UNCHANGED: "Presente sem alteração relevante", IMPROVED: "Condição melhorada", NOT_OBSERVED: "Não observado" };
    for (const [status, label] of Object.entries(legacy)) {
      expect(inventoryEntryStatusLabel({ status, statusLabel: null })).toBe(label);
      expect(inventoryEntryStatusLabel({ status })).toBe(inventoryStatusLabel(status));
    }
    for (const html of screens(inventoryOf([{ name: "Pia", status: "MISSING", statusLabel: null }]))) {
      expect(html).toContain("Não encontrado");
    }
  });
});
