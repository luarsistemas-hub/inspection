import { describe, expect, it } from "vitest";
import { act, createElement, type ComponentProps } from "react";
import { createRoot } from "react-dom/client";
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
      evidence: { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } },
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

  it("IT-204 explains current-only reports without rendering an origin column", () => {
    const report = {
      id: "report-v2", version: 2, classification: "ATTENTION", advisory: "Análise atual.",
      context: { asset: { name: "Imóvel QA", address: "Rua QA", externalKey: "ASSET-1" }, participant: { name: "QA" }, template: { name: "Padrão", version: 1 }, inspection: { stageLabel: null } },
      requirements: [{ key: "video:kitchen", section: "Cozinha", label: "Cozinha", instructions: null, noRelevantChange: null, analysisMode: "CURRENT_ONLY", analysisStatus: "COMPLETED" }],
      evidence: { nodes: [{ id: "F11", requirementKey: "video:kitchen", role: "CURRENT", description: "Frame atual", capturedAt: null, flags: [], availability: "AVAILABLE", url: null, evidenceKind: "VIDEO_FRAME", frameId: "F11", timestampMs: 1000, sourceMediaId: "video-2", component: "sink", ptsMicros: null, label: null, sourceAvailability: null }], pageInfo: { endCursor: null, hasNextPage: false } },
      findings: [],
    } as unknown as ComponentProps<typeof ReportVisual>["report"];

    const document = new DOMParser().parseFromString(renderToStaticMarkup(createElement(ReportVisual, { report, onDownload: () => {} })), "text/html");
    expect(document.querySelector(".report-no-baseline")?.textContent).toContain("não há referência histórica para comparar");
    expect(document.querySelectorAll(".report-evidence-column h4")).toHaveLength(1);
    expect(document.querySelector(".report-visual")?.textContent).not.toContain("ORIGEM");
  });

  it("IT-204 opens the V2 before/after report section without an origin column", async () => {
    const report = {
      id: "report-v2", version: 2, classification: "ATTENTION", advisory: "Análise atual.",
      context: { asset: { name: "Imóvel QA", address: "Rua QA", externalKey: "ASSET-1" }, participant: { name: "QA" }, template: { name: "Padrão", version: 1 }, inspection: { stageLabel: null } },
      requirements: [
        { key: "photo:overview", section: "Entrada", label: "Visão geral", instructions: null, noRelevantChange: null, analysisMode: "COMPLETED", analysisStatus: "COMPLETED" },
        { key: "video:kitchen", section: "Cozinha", label: "Cozinha", instructions: null, noRelevantChange: null, analysisMode: "CURRENT_ONLY", analysisStatus: "COMPLETED" },
      ],
      evidence: { nodes: [{ id: "F11", requirementKey: "video:kitchen", role: "CURRENT", description: "Frame atual", capturedAt: null, flags: [], availability: "AVAILABLE", url: null, evidenceKind: "VIDEO_FRAME", frameId: "F11", timestampMs: 1000, sourceMediaId: "video-2", component: "sink", ptsMicros: null, label: null, sourceAvailability: null }], pageInfo: { endCursor: null, hasNextPage: false } },
      findings: [],
    } as unknown as ComponentProps<typeof ReportVisual>["report"];
    const host = document.createElement("div");
    const root = createRoot(host);
    await act(async () => { root.render(createElement(ReportVisual, { report, onDownload: () => {} })); });
    const sectionButton = [...host.querySelectorAll(".report-section-nav button")].find((button) => button.textContent?.includes("Cozinha"));
    expect(sectionButton).toBeDefined();
    await act(async () => { sectionButton?.dispatchEvent(new MouseEvent("click", { bubbles: true })); });
    expect(host.querySelector(".report-no-baseline")?.textContent).toContain("não há referência histórica para comparar");
    expect(host.querySelectorAll(".report-evidence-column h4")).toHaveLength(1);
    expect(host.querySelector(".report-visual")?.textContent).not.toContain("ORIGEM");
    await act(async () => { root.unmount(); });
  });
});
