import { describe, expect, it } from "vitest";
import { createElement, type ComponentProps } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { ReportVisual, unmatchedReportEvidence } from "@/features/dashboard/report-visual";

describe("Report evidence presentation", () => {
  it("keeps evidence with legacy or unknown requirement keys visible", () => {
    const evidence = [
      { id: "reference", requirementKey: "Referência" },
      { id: "current", requirementKey: "origin:current" },
      { id: "matched", requirementKey: "overview" },
    ];

    expect(unmatchedReportEvidence([{ key: "overview" }], evidence).map(({ id }) => id)).toEqual(["reference", "current"]);
  });

  it("keeps the report summary compact and publication controls permission-bound", () => {
    const report = {
      id: "report-1",
      version: 1,
      classification: "ATTENTION",
      advisory: "Esta triagem interna descreve alterações observadas.",
      context: { asset: { name: "Rua Gustavo Zimmer 227", address: "Rua Gustavo Zimmer, 227 — Florianópolis/SC", externalKey: "ASSET-1" }, participant: { name: "Maria" }, template: { name: "Padrão", version: 1 }, inspection: { stageLabel: "Entrada" } },
      jsonDigest: "json", htmlDigest: "html", canonicalJSON: {},
      requirements: [],
      evidence: [],
      findings: [],
    } as unknown as ComponentProps<typeof ReportVisual>["report"];

    const html = renderToStaticMarkup(createElement(ReportVisual, { report, onDownload: () => {} }));
    const document = new DOMParser().parseFromString(html, "text/html");

    expect(document.querySelectorAll(".report-address")).toHaveLength(1);
    expect(document.querySelector(".report-address")?.textContent).toBe(report.context.asset.address);
    expect(document.querySelector(".report-advisory-info summary")?.getAttribute("aria-label")).toBe("Sobre a triagem interna");
    expect(document.querySelector(".report-advisory-info")?.hasAttribute("open")).toBe(false);
    expect(document.querySelector(".report-advisory-info p")?.textContent).toBe(report.advisory);
    expect(document.querySelector(".report-publication-menu")).toBeNull();
    expect(document.querySelector(".report-visual")?.textContent).not.toContain("Informações técnicas");
    expect(document.querySelector(".report-visual")?.textContent).not.toMatch(/Digest (?:canônico|renderizado|do PDF)/);
    expect(document.querySelector(".report-visual pre")).toBeNull();
    expect(document.querySelector(".report-details summary")?.textContent).toBe("Dados do imóvel e da vistoria");

    const managerHtml = renderToStaticMarkup(createElement(ReportVisual, { report, onDownload: () => {}, onPublish: () => {}, onInvalidatePublication: () => {} }));
    const managerDocument = new DOMParser().parseFromString(managerHtml, "text/html");
    expect(managerDocument.querySelector(".report-publication-menu summary")?.getAttribute("aria-label")).toBe("Gerenciar publicação");
  });
});
