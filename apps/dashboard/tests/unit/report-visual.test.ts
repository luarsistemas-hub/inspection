import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { act, createElement, type ComponentProps } from "react";
import { createRoot } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { CustomerChecklistView, CustomerReportVisual, ReportVisual, seekVideoToEvidence, unmatchedReportEvidence, videoContentUrl } from "@/features/dashboard/report-visual";
import { buildReportRooms } from "@/features/dashboard/report-room-model";
import { ReportVersionHistory } from "@/features/dashboard/reports-journey";

const customerChecklistGolden = JSON.parse(readFileSync(resolve(__dirname, "../../../../services/inspection/internal/features/reports/customer/core/testdata/customer_report_v3.golden.json"), "utf8")) as { coverage: unknown; rooms: Array<unknown> };
const internalChecklistGolden = JSON.parse(readFileSync(resolve(__dirname, "../../../../services/inspection/internal/platform/graphql/resolvers/testdata/internal_report_v3.golden.json"), "utf8")) as { coverage: unknown; rooms: Array<unknown> };

describe("Report evidence presentation", () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

  it("keeps evidence with legacy or unknown requirement keys visible", () => {
    const evidence = [{ id: "reference", requirementKey: "Referência" }, { id: "current", requirementKey: "origin:current" }, { id: "matched", requirementKey: "overview" }];
    expect(unmatchedReportEvidence([{ key: "overview" }], evidence).map(({ id }) => id)).toEqual(["reference", "current"]);
  });

  it("groups video scopes under their actual room and keeps each repeated scope's evidence separate", async () => {
    const report = reportWith([
      { ...requirement("main", "vistoria", "Vídeo principal"), key: "video:main", instructions: "Grave um vídeo contínuo." },
      requirement("lareira", "Vídeo", "Sala de estar — Lareira"),
      requirement("parede-1", "Vídeo", "Sala de estar — Parede de destaque em pedra", "Para recaptura: filme Sala de estar — Parede de destaque em pedra de perto, mostrando a vista parede e canto.", "CURRENT_ONLY", "INCONCLUSIVE"),
      requirement("parede-2", "Vídeo", "Sala de estar — Parede de destaque em pedra", "Para recaptura: filme Sala de estar — Parede de destaque em pedra de perto, mostrando a vista wall_and_window.", "CURRENT_ONLY", "INCONCLUSIVE"),
    ], [
      evidence("lareira-atual", "video:lareira", "CURRENT", "/lareira.jpg", "VIDEO_FRAME", "lareira", 17_000),
      evidence("parede-1-origem", "video:parede-1", "REFERENCE", "/parede-1.jpg", "VIDEO_FRAME", "origin", 3_000),
      evidence("parede-2-origem", "video:parede-2", "REFERENCE", "/parede-2.jpg", "VIDEO_FRAME", "origin", 4_000),
    ], []);
    const { rooms, captureInstructions } = buildReportRooms(report);
    expect(rooms.map((room) => room.name)).toEqual(["Sala de estar"]);
    expect(rooms[0].items.map((item) => item.title)).toEqual(["Parede de destaque em pedra", "Lareira"]);
    expect(rooms[0].items[0].entries.map((entry) => entry.evidence.map((item) => item.id))).toEqual([["parede-1-origem"], ["parede-2-origem"]]);
    expect(rooms[0].items[0].instructions).toHaveLength(2);
    expect(rooms[0].items[0].instructions[0]).toBe("Filme de perto, mostrando a vista parede e canto.");
    expect(rooms[0].items[0].instructions[1]).toContain("parede e janela");
    expect(captureInstructions).toEqual(["Grave um vídeo contínuo."]);
    const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => { root.render(createElement(ReportVisual, { report, onDownload: () => {} })); });
    await act(async () => { host.querySelector<HTMLButtonElement>(".report-room-toggle")?.click(); });
    expect(host.querySelectorAll(".report-room")).toHaveLength(1);
    expect(host.querySelectorAll(".report-room-requirement--compact")).toHaveLength(2);
    expect(host.querySelectorAll(".report-compact-copy p")).toHaveLength(2);
    expect(host.textContent).toContain("Mais 1 vista ao ver as evidências.");
    await act(async () => { host.querySelector<HTMLButtonElement>(".report-evidence-toggle")?.click(); });
    expect(host.querySelectorAll(".report-recapture-views li")).toHaveLength(2);
    expect(host.querySelectorAll(".report-evidence-pair")).toHaveLength(2);
    await act(async () => { root.unmount(); });
  });

  it("keeps the report summary compact and publication controls permission-bound", () => {
    const report = baseReport({ requirements: [], evidence: { nodes: [], pageInfo: { endCursor: null, hasNextPage: false } }, findings: [] });
    const document = new DOMParser().parseFromString(renderToStaticMarkup(createElement(ReportVisual, { report, onDownload: () => {} })), "text/html");
    expect(document.querySelectorAll(".report-address")).toHaveLength(1);
    expect(document.querySelector(".report-advisory-info")?.hasAttribute("open")).toBe(false);
    expect(document.querySelector(".report-publication-menu")).toBeNull();
    expect(document.querySelector(".report-details summary")?.textContent).toBe("Dados do imóvel e da vistoria");
    const managerDocument = new DOMParser().parseFromString(renderToStaticMarkup(createElement(ReportVisual, { report, onDownload: () => {}, onPublish: () => {}, onInvalidatePublication: () => {} })), "text/html");
    expect(managerDocument.querySelector(".report-publication-menu summary")?.getAttribute("aria-label")).toBe("Gerenciar publicação");
  });

  it("switches between compact cards and a list without hiding inventory details", async () => {
    const report = {
      ...reportWith([], [], []),
      inventory: {
        referenceVersionNumber: 4,
        purpose: "INVENTORY_UPDATE",
        coverage: { expectedItems: 2, assessedItems: 1, notObserved: 1, noBasis: 0, environmentsNotCaptured: [], environmentsAddedDuringCapture: [] },
        problems: [
          { environment: "Cozinha", type: "CABINET", name: "Armários planejados", status: "NOT_OBSERVED", statusLabel: null, conditionBefore: "GOOD", conditionAfter: null, reason: "Não foi encontrada uma vista correspondente.", pairs: [] },
          { environment: "Cozinha", type: "SINK", name: "Pia", status: "MISSING", statusLabel: null, conditionBefore: "GOOD", conditionAfter: null, reason: "Item não encontrado.", pairs: [] },
        ],
        informational: [],
      },
    } as ComponentProps<typeof ReportVisual>["report"];
    const host = document.createElement("div");
    const root = createRoot(host);
    await act(async () => { root.render(createElement(ReportVisual, { report, onDownload: () => {} })); });
    expect(host.querySelector(".report-inventory--cards")).not.toBeNull();
    expect(host.querySelector('[aria-label="Visualização dos itens"]')).not.toBeNull();
    const hiddenItems = host.querySelector<HTMLDetailsElement>(".report-inventory-disclosure");
    expect(hiddenItems?.open).toBe(false);
    expect(hiddenItems?.querySelector("summary")?.textContent).toBe("Itens não observados (1)");
    expect(host.querySelector(".report-inventory-items")?.textContent).toContain("Pia");
    expect(host.querySelector(".report-inventory-items")?.textContent).not.toContain("Armários planejados");
    await act(async () => { hiddenItems?.querySelector("summary")?.click(); });
    expect(hiddenItems?.open).toBe(true);
    expect(host.textContent).toContain("Não foi encontrada uma vista correspondente.");
    const listButton = [...host.querySelectorAll<HTMLButtonElement>(".report-inventory-layout button")].find((button) => button.textContent === "Lista");
    await act(async () => { listButton?.click(); });
    expect(host.querySelector(".report-inventory--list")).not.toBeNull();
    expect(listButton?.getAttribute("aria-pressed")).toBe("true");
    expect(host.textContent).toContain("Armários planejados");
    await act(async () => { hiddenItems?.querySelector("summary")?.click(); });
    expect(hiddenItems?.open).toBe(false);
    await act(async () => { root.unmount(); });
  });

  it("UT-354 and UT-356 render a NORMAL v2 summary and separate added environments", () => {
    const report = { ...reportWith([], [], []), classification: "NORMAL", inventory: { referenceVersionNumber: 2, purpose: "MOVE_IN", coverage: { expectedItems: 24, assessedItems: 18, notObserved: 0, noBasis: 0, environmentsNotCaptured: [], environmentsAddedDuringCapture: ["Depósito"] }, problems: [], informational: [] } } as ComponentProps<typeof ReportVisual>["report"];
    const markup = renderToStaticMarkup(createElement(ReportVisual, { report, onDownload: () => {} }));
    expect(markup).toContain("Sem alertas no inventário");
    expect(markup).toContain("18 de 24 itens avaliados");
    expect(markup).toContain("Ambientes adicionados nesta vistoria: Depósito");
    expect(markup).not.toContain("report-room-list");
    expect(markup).not.toContain("Informativos");
    const video = { id: "current", requirementKey: "room", role: "CURRENT", flags: [], availability: "AVAILABLE", sourceMediaId: "source/1", snapshotId: "snapshot-7", timestampMs: 12_500 };
    expect(videoContentUrl(video)).toBe("/api/media/videos/source%2F1/content?snapshotId=snapshot-7");
    expect(videoContentUrl(video)).not.toContain("?t=");
    const seekTarget = { duration: 30, currentTime: 0 };
    expect(seekVideoToEvidence(seekTarget, 12_500)).toBe(true);
    expect(seekTarget.currentTime).toBe(12.5);
  });

  it("UT-354 renders problem evidence as an origin/current pair with both timestamps", () => {
    const report = {
      ...reportWith([], [
        evidence("origin-frame", "inventory:item-a", "REFERENCE", "/origin.jpg", "VIDEO_FRAME", "origin-video", 5_000),
        evidence("current-frame", "inventory:item-a", "CURRENT", "/current.jpg", "VIDEO_FRAME", "current-video", 17_500),
      ], []),
      inventory: {
        referenceVersionNumber: 2,
        purpose: "MOVE_IN",
        coverage: null,
        problems: [{ environment: "Sala", type: "SOFA", name: "Sofá", status: "CONDITION_CHANGED", statusLabel: null, conditionBefore: "GOOD", conditionAfter: "POOR", reason: "Rasgo visível", pairs: [{ originEvidenceId: "origin-frame", currentEvidenceId: "current-frame", originSourceAvailability: "EXPIRED", currentSourceAvailability: "AVAILABLE", timestampMs: 17_500 }] }],
        informational: [],
      },
    } as ComponentProps<typeof ReportVisual>["report"];
    const markup = renderToStaticMarkup(createElement(ReportVisual, { report, onDownload: () => {} }));
    const document = new DOMParser().parseFromString(markup, "text/html");
    const pair = document.querySelector(".report-inventory-pair");
    expect(pair?.children).toHaveLength(2);
    expect(pair?.children[0]?.textContent).toContain("Na origem");
    expect(pair?.children[0]?.textContent).toContain("Momento 00:05");
    expect(pair?.children[0]?.textContent).toContain("Original não disponível; quadro preservado");
    expect(pair?.children[1]?.textContent).toContain("Na vistoria atual");
    expect(pair?.children[1]?.textContent).toContain("Momento 00:17");
  });

  it("starts with all rooms collapsed and does not mount any images until a room is expanded", async () => {
    const report = reportWith([
      requirement("kitchen", "Cozinha", "Pia"),
      requirement("living", "Sala de estar", "Lareira"),
    ], [
      evidence("kitchen-frame", "video:kitchen", "CURRENT", "/kitchen.jpg"),
      evidence("living-frame", "video:living", "CURRENT", "/living.jpg"),
    ], [{ title: "Fissura no revestimento", description: "Fissura lateral", severity: "MEDIUM", recommendedAction: "Verificar", evidenceIds: ["living-frame"] }]);
    const host = document.createElement("div");
    const root = createRoot(host);
    await act(async () => { root.render(createElement(ReportVisual, { report, onDownload: () => {} })); });

    expect(host.querySelectorAll('.report-room-toggle[aria-expanded="false"]')).toHaveLength(2);
    expect(host.querySelectorAll("img")).toHaveLength(0);
    expect(host.querySelectorAll("video")).toHaveLength(0);
    expect(host.textContent).toContain("Fissura no revestimento");
    const sala = [...host.querySelectorAll<HTMLButtonElement>(".report-room-toggle")].find((button) => button.textContent?.includes("Sala de estar"));
    await act(async () => { sala?.click(); });
    expect(host.querySelectorAll("img")).toHaveLength(1);
    expect(host.querySelectorAll(".report-pair-play")).toHaveLength(0);
    expect(host.textContent).toContain("Fissura lateral");

    await act(async () => { sala?.click(); });
    await act(async () => { sala?.click(); });
    expect(host.querySelectorAll("img")).toHaveLength(1);
    await act(async () => { root.unmount(); });
  });

  it("shows compact instructions without verbose state and loads its evidence only on request", async () => {
    const report = reportWith([requirement("pending", "Sala", "Lareira", "Filme de perto, com boa luz e sem obstrução.", "CURRENT_ONLY", "INCONCLUSIVE")], [
      evidence("origin", "video:pending", "REFERENCE", "/origin.jpg"),
      evidence("current", "video:pending", "CURRENT", "/current.jpg"),
    ], []);
    const host = document.createElement("div");
    const root = createRoot(host);
    await act(async () => { root.render(createElement(ReportVisual, { report, onDownload: () => {} })); });
    const room = host.querySelector<HTMLButtonElement>(".report-room-toggle");
    await act(async () => { room?.click(); });
    expect(host.textContent).toContain("Lareira");
    expect(host.textContent).toContain("Filme de perto, com boa luz e sem obstrução.");
    expect(host.textContent).not.toContain("A origem existe neste laudo");
    expect(host.querySelectorAll("img")).toHaveLength(0);
    const evidenceToggle = host.querySelector<HTMLButtonElement>(".report-evidence-toggle");
    await act(async () => { evidenceToggle?.click(); });
    expect(host.querySelectorAll("img")).toHaveLength(2);
    expect(host.textContent).toContain("Origem disponível; comparação não concluída.");
    await act(async () => { root.unmount(); });
  });

  it("renders one play control per paired video frame and hides frame identifiers", async () => {
    const report = reportWith([requirement("living", "Sala", "Lareira")], [
      evidence("origin-id", "video:living", "REFERENCE", "/origin.jpg", "VIDEO_FRAME", "origin-video", 11_000),
      evidence("current-id", "video:living", "CURRENT", "/current.jpg", "VIDEO_FRAME", "current-video", 17_000),
    ], [{ title: "Fissura", description: "Na lateral", severity: "MEDIUM", recommendedAction: "Avaliar", evidenceIds: ["current-id"] }]);
    const host = document.createElement("div");
    const root = createRoot(host);
    await act(async () => { root.render(createElement(ReportVisual, { report, onDownload: () => {} })); });
    const room = host.querySelector<HTMLButtonElement>(".report-room-toggle");
    await act(async () => { room?.click(); });
    expect(host.querySelectorAll(".report-pair-play")).toHaveLength(1);
    expect(host.textContent).not.toContain("origin-i");
    expect(host.textContent).not.toContain("PTS");
    expect(host.textContent).not.toContain("Frame origin-id");
    const play = host.querySelector<HTMLButtonElement>(".report-pair-play");
    expect(play?.getAttribute("aria-label")).toContain("00:17");
    await act(async () => { root.unmount(); });
  });

  it("supports an origin-only video play control for a one-sided pair", async () => {
    const report = reportWith([requirement("origin", "Entrada", "Porta")], [evidence("origin-video-frame", "video:origin", "REFERENCE", "/origin.jpg", "VIDEO_FRAME", "origin-video", 4_000)], []);
    const host = document.createElement("div");
    const root = createRoot(host);
    await act(async () => { root.render(createElement(ReportVisual, { report, onDownload: () => {} })); });
    await act(async () => { host.querySelector<HTMLButtonElement>(".report-room-toggle")?.click(); });
    await act(async () => { host.querySelector<HTMLButtonElement>(".report-evidence-toggle")?.click(); });
    expect(host.querySelectorAll(".report-pair-play")).toHaveLength(1);
    expect(host.querySelector(".report-pair-play")?.getAttribute("aria-label")).toContain("00:04");
    await act(async () => { root.unmount(); });
  });

  it("keeps one play control for each row when a requirement has multiple video frames", async () => {
    const report = reportWith([requirement("living", "Sala", "Lareira")], [
      evidence("origin-1", "video:living", "REFERENCE", "/origin-1.jpg", "VIDEO_FRAME", "origin-video", 1_000),
      evidence("origin-2", "video:living", "REFERENCE", "/origin-2.jpg", "VIDEO_FRAME", "origin-video", 2_000),
      evidence("current-1", "video:living", "CURRENT", "/current-1.jpg", "VIDEO_FRAME", "current-video", 3_000),
      evidence("current-2", "video:living", "CURRENT", "/current-2.jpg", "VIDEO_FRAME", "current-video", 4_000),
    ], [{ title: "Fissura", description: "Na lateral", severity: "MEDIUM", recommendedAction: "Avaliar", evidenceIds: ["current-1"] }]);
    const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => { root.render(createElement(ReportVisual, { report, onDownload: () => {} })); });
    await act(async () => { host.querySelector<HTMLButtonElement>(".report-room-toggle")?.click(); });
    expect(host.querySelectorAll(".report-evidence-pair")).toHaveLength(2);
    expect(host.querySelectorAll(".report-pair-play")).toHaveLength(2);
    await act(async () => { root.unmount(); });
  });

  it("UT-100 shows a sound catalog item and its evidence for a NORMAL report with no findings", () => {
    const report = {
      ...reportWith([], [evidence("current-photo", "room:cabinets", "CURRENT", "/cabinet.jpg")], []),
      classification: "NORMAL", findings: [],
      checklist: internalChecklistGolden,
    } as unknown as ComponentProps<typeof ReportVisual>["report"];
    const markup = renderToStaticMarkup(createElement(ReportVisual, { report, onDownload: () => {} }));
    expect(markup).toContain("Armários");
    expect(markup).toContain('src="/cabinet.jpg"');
    expect(markup).toContain('alt="Imagem da evidência"');
    expect(markup).toContain("avaliação completa: 1 de 1 componentes");
    expect(markup).not.toContain("Todos os itens esperados foram verificados");
  });

  it("UT-101 marks analysis failure and never calls an empty result alert-free", () => {
    const report = { ...reportWith([], [], []), classification: "NORMAL", findings: [], processing: { status: "FAILED", message: "A análise não pôde ser concluída." } } as unknown as ComponentProps<typeof ReportVisual>["report"];
    const markup = renderToStaticMarkup(createElement(ReportVisual, { report, onDownload: () => {} }));
    expect(markup).toContain("Falha na análise");
    expect(markup).toContain("A análise não pôde ser concluída.");
    expect(markup).not.toContain("Sem alertas no inventário");
    expect(markup).not.toContain("Sem alertas identificados");
  });

  it("UT-102 keeps customer checklist allowlisted and excludes confidence and rejected suggestions", () => {
    const report = {
      inspectionId: "inspection", snapshotId: "snapshot", version: 3, classification: "NORMAL", classificationLabel: "Sem alertas identificados", limited: false, advisory: "Relatório descritivo.", status: "PUBLISHED", historical: false,
      context: { asset: { id: "asset", name: "Imóvel QA", externalKey: "QA", address: "Rua QA" }, participant: { name: "Pessoa QA" }, template: { id: "template", name: "Modelo", version: 3 }, inspection: { projectId: null, stageId: null, stageLabel: null, dueAt: null, submittedAt: null, generatedAt: "2026-10-01T00:00:00Z" } },
      requirements: [], inventory: null, processing: null, handover: null, comparison: null,
      checklist: { ...customerChecklistGolden, rooms: customerChecklistGolden.rooms.map((room) => ({ ...(room as object), components: [{ ...((room as { components: Array<object> }).components[0]), confidence: 0.99, rejectedSuggestion: "INTERNAL REJECTED TEXT" }] })) },
    } as unknown as Parameters<typeof CustomerReportVisual>[0]["report"];
    const markup = renderToStaticMarkup(createElement(CustomerReportVisual, { report, evidence: [] }));
    expect(markup).toContain("Armários íntegros nas vistas disponíveis.");
    expect(markup).toContain("Observado");
    expect(markup).toContain("Condição observada: Bom");
    expect(markup).toContain("<figcaption>E001</figcaption>");
    expect(markup).not.toContain("Sugestão rejeitada pela equipe");
    expect(markup).not.toContain("confidence");
    expect(markup).not.toContain("0.99");
    expect(markup).not.toContain("INTERNAL REJECTED TEXT");
  });

  it("renders backend golden scope, reasons, notice, assessment extent and attribute provenance", () => {
    const report = {
      ...reportWith([], [evidence("current-photo", "room:kitchen", "CURRENT", "/cabinet.jpg")], []),
      checklist: internalChecklistGolden,
      scopeStatements: ["Cozinha e armários"], reasonLabels: ["Vistoria de saída"],
      evidenceNotice: "As evidências disponíveis são limitadas.",
    } as unknown as ComponentProps<typeof ReportVisual>["report"];
    const markup = renderToStaticMarkup(createElement(ReportVisual, { report, onDownload: () => {} }));
    expect(markup).toContain("Escopo da análise");
    expect(markup).toContain("Cozinha e armários");
    expect(markup).toContain("Motivos");
    expect(markup).toContain("Vistoria de saída");
    expect(markup).toContain("As evidências disponíveis são limitadas.");
    expect(markup).toContain("Extensão avaliada: Frentes e laterais");
    expect(markup).toContain("Fonte visual · descrição · evidência E001");
  });

  it("shows the backend formatted handover values without raw enums", () => {
    const report = { ...reportWith([], [], []), handover: { entries: [{ kind: "METER", status: "DECLARED", payload: { presentation: "Medidor · Declarado · Água · 42 · m³" }, evidenceIds: [], declarant: null }] } } as unknown as ComponentProps<typeof ReportVisual>["report"];
    const markup = renderToStaticMarkup(createElement(ReportVisual, { report, onDownload: () => {} }));
    expect(markup).toContain("Medidor · Declarado · Água · 42 · m³");
    expect(markup).not.toContain("WATER");
    expect(markup).not.toContain("Registrado");
    expect(markup).not.toContain("Declarado por:");
  });

  it("UT-103 says scope is undefined without inventing a coverage percentage", () => {
    const report = { ...reportWith([], [], []), checklist: { coverage: { expected: 0, fullyAssessed: 0, partiallyAssessed: 0, notObserved: 0, notApplicable: 0, impossible: 0, unknownScope: true }, rooms: [] } } as unknown as ComponentProps<typeof ReportVisual>["report"];
    const markup = renderToStaticMarkup(createElement(ReportVisual, { report, onDownload: () => {} }));
    expect(markup).toContain("Escopo não definido; não há percentual de cobertura.");
    expect(markup).not.toContain("0%");
  });

  it("renders the concise condition wording shared with the v3 report PDF", () => {
    const report = { ...reportWith([], [], []), checklist: internalChecklistGolden } as unknown as ComponentProps<typeof ReportVisual>["report"];
    const markup = renderToStaticMarkup(createElement(ReportVisual, { report, onDownload: () => {} }));
    expect(markup).toContain("Condição observada: Bom");
    expect(markup).not.toContain("Condição: Bom");
  });

  it("never requests or renders customer checklist media unless availability is AVAILABLE", () => {
    const checklist = JSON.parse(JSON.stringify(customerChecklistGolden)) as NonNullable<NonNullable<Parameters<typeof CustomerReportVisual>[0]["report"]["checklist"]>>;
    checklist.rooms[0].components[0].evidenceIds = ["E001", "E002", "E003", "E004"];
    const evidence = [
      { id: "available", reference: "E001", mediaAvailability: "AVAILABLE", state: "AVAILABLE", url: "/private/available.jpg" },
      { id: "withheld", reference: "E002", mediaAvailability: "WITHHELD", state: "WITHHELD", url: null },
      { id: "unavailable", reference: "E003", mediaAvailability: "UNAVAILABLE", state: "UNAVAILABLE", url: "/private/unavailable.jpg" },
      { id: "revoked", reference: "E004", mediaAvailability: "REVOKED", state: "REVOKED", url: "/private/revoked.jpg" },
    ] as unknown as Parameters<typeof CustomerChecklistView>[0]["evidence"];
    const markup = renderToStaticMarkup(createElement(CustomerChecklistView, { checklist, evidence, snapshotId: "snapshot" }));
    expect(markup.match(/<img\b/g)).toHaveLength(1);
    expect(markup).toContain("/private/available.jpg");
    expect(markup).not.toContain("/private/unavailable.jpg");
    expect(markup).not.toContain("/private/revoked.jpg");
    for (const reference of ["E001", "E002", "E003", "E004"]) expect(markup).toContain(`<figcaption>${reference}</figcaption>`);
  });

  it("UT-104 filters comparison items while retaining the baseline denominator", async () => {
    const items = Array.from({ length: 20 }, (_, index) => ({ itemIdentityId: `item-${index}`, room: "Sala", typeCode: "SOFA", name: `Sofá ${index + 1}`, outcome: index < 6 ? "CHANGED" : "UNCHANGED", outcomeLabel: index < 6 ? "Alterado" : "Sem alteração", baselineCondition: "GOOD", currentCondition: "FAIR", description: null, limitations: [], pairs: [], pairingRevised: false }));
    const report = { ...reportWith([], [], []), comparison: { baseline: { versionId: "baseline", versionNumber: 4, purpose: "MOVE_IN", purposeLabel: "Entrada no imóvel", activatedAt: "2026-10-01T00:00:00Z" }, status: "COMPLETED", statusLabel: "Comparação concluída", counts: { total: 20, unchanged: 14, changed: 6, improved: 0, missing: 0, notObserved: 0, noBasis: 0 }, items, outsideBaseline: [] } } as unknown as ComponentProps<typeof ReportVisual>["report"];
    const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => { root.render(createElement(ReportVisual, { report, onDownload: () => {} })); });
    const changed = [...host.querySelectorAll<HTMLButtonElement>(".report-comparison-filters button")].find((button) => button.textContent?.startsWith("Alterado"));
    await act(async () => { changed?.click(); });
    expect(host.querySelectorAll(".report-v3-comparison-item")).toHaveLength(6);
    expect(host.querySelector(".report-v3-comparison > header")?.textContent).toContain("6 de 20 itens");
    expect(host.textContent).toContain("Referência · versão 4");
    await act(async () => { root.unmount(); });
  });

  it("disables reissue and pairing controls while another report version is pending", () => {
    const report = {
      ...reportWith([], [evidence("reference", "inventory", "REFERENCE", "/reference.jpg"), evidence("current", "inventory", "CURRENT", "/current.jpg")], []),
      comparison: { baseline: { versionId: "baseline", versionNumber: 4, purpose: "MOVE_IN", purposeLabel: "Entrada no imóvel", activatedAt: "2026-10-01T00:00:00Z" }, status: "COMPLETED", statusLabel: "Comparação concluída", counts: { total: 1, unchanged: 0, changed: 1, improved: 0, missing: 0, notObserved: 0, noBasis: 0 }, items: [{ itemIdentityId: "item", room: "Sala", typeCode: "SOFA", name: "Sofá", outcome: "CHANGED", outcomeLabel: "Alterado", baselineCondition: "GOOD", currentCondition: "FAIR", description: null, limitations: [], pairs: [{ baselineEvidenceId: "reference", currentEvidenceId: "current" }], pairingRevised: false }], outsideBaseline: [],
    },
    } as unknown as ComponentProps<typeof ReportVisual>["report"];
    const markup = renderToStaticMarkup(createElement(ReportVisual, { report, onDownload: () => {}, onReissue: () => {}, canRevisePairing: true, onRevisePairing: async () => {}, actionsDisabled: true }));
    expect(markup).toMatch(/<button[^>]*disabled=""[^>]*>Emitir nova versão<\/button>/);
    expect(markup).toMatch(/<select[^>]*disabled=""/);
    expect(markup).toMatch(/<button[^>]*disabled=""[^>]*>Salvar revisão<\/button>/);
  });

  it("UT-105 labels a missing baseline view as no basis without rendering a null baseline condition", () => {
    const comparison = { baseline: { versionId: "baseline", versionNumber: 2, purpose: "MOVE_IN", purposeLabel: "Entrada", activatedAt: "2026-10-01T00:00:00Z" }, status: "COMPLETED", statusLabel: "Comparação concluída", counts: { total: 1, unchanged: 0, changed: 0, improved: 0, missing: 0, notObserved: 0, noBasis: 1 }, items: [{ itemIdentityId: "item", room: "Cozinha", typeCode: "CABINET", name: "Armário", outcome: "NO_BASIS", outcomeLabel: "Sem base para comparar", baselineCondition: null, currentCondition: "GOOD", description: null, limitations: [], pairs: [], pairingRevised: false }], outsideBaseline: [] };
    const report = { ...reportWith([], [], []), comparison } as unknown as ComponentProps<typeof ReportVisual>["report"];
    const markup = renderToStaticMarkup(createElement(ReportVisual, { report, onDownload: () => {} }));
    expect(markup).toContain("Sem base para comparar");
    expect(markup).toContain("Não há uma vista avaliável do item na referência aprovada");
    expect(markup).not.toContain("Condição da referência: —");
  });

  it("UT-106 shows unavailable customer evidence without exposing media URLs", () => {
    const report = {
      inspectionId: "inspection", snapshotId: "snapshot", version: 3, classification: "ATTENTION", classificationLabel: "Requer atenção", limited: true, advisory: "Comparação limitada.", status: "PUBLISHED", historical: false,
      context: { asset: { id: "asset", name: "Imóvel QA", externalKey: "QA", address: "Rua QA" }, participant: { name: "Pessoa QA" }, template: { id: "template", name: "Modelo", version: 3 }, inspection: { projectId: null, stageId: null, stageLabel: null, dueAt: null, submittedAt: null, generatedAt: "2026-10-01T00:00:00Z" } },
      requirements: [], inventory: null, checklist: null, processing: null, handover: null,
      comparison: { baselineVersionNumber: 1, purpose: "Entrada no imóvel", purposeLabel: "Entrada no imóvel", activatedAt: "2026-09-01T00:00:00Z", status: "COMPLETED", statusLabel: "Comparação concluída", counts: { total: 1, unchanged: 0, changed: 0, improved: 0, missing: 0, notObserved: 1, noBasis: 0 }, displayed: 1, disclaimer: "A comparação descreve evidências disponíveis e não atribui responsabilidades nem custos.", items: [{ room: "Sala", name: "Sofá", outcome: "NOT_OBSERVED", outcomeLabel: "Não observado", baselineCondition: "Bom", currentCondition: null, limitations: ["Evidência atual indisponível."], pairs: [{ baselineEvidence: "E001", currentEvidence: "E002", baselineAvailability: "UNAVAILABLE", currentAvailability: "UNAVAILABLE" }] }], outsideBaseline: [] },
    } as unknown as Parameters<typeof CustomerReportVisual>[0]["report"];
    const markup = renderToStaticMarkup(createElement(CustomerReportVisual, { report, evidence: [] }));
    expect(markup).toContain("Evidência da referência indisponível");
    expect(markup).toContain("Não observado");
    expect(markup).not.toContain("https://");
    expect(markup).not.toContain("<img");
    expect(markup).toContain("E001 ↔ E002");
    expect(markup).toContain("Referência · E001");
    expect(markup).toContain("Vistoria atual · E002");
    expect(markup).not.toContain("S3");
  });

  it("UT-116 renders version history from backend scope and review labels", () => {
    const history = [
      { id: "snapshot-2", inspectionId: "inspection", assetName: "Imóvel QA", assetAddress: "Rua QA", assetExternalKey: "QA", participantName: "Pessoa QA", generatedAt: "2026-10-02T12:00:00Z", classification: "ATTENTION", version: 2, scopeLabel: "4 componentes no escopo", reviewLabel: "Não revisado", publication: { id: "publication-2", snapshotId: "snapshot-2", inspectionId: "inspection", status: "PUBLISHED", version: 2, publishedAt: "2026-10-02T12:00:00Z", invalidatedAt: null } },
      { id: "snapshot-1", inspectionId: "inspection", assetName: "Imóvel QA", assetAddress: "Rua QA", assetExternalKey: "QA", participantName: "Pessoa QA", generatedAt: "2026-10-01T12:00:00Z", classification: "NORMAL", version: 1, scopeLabel: "Escopo não definido", reviewLabel: "Falha na análise", publication: null },
    ] as unknown as ComponentProps<typeof ReportVersionHistory>["history"];
    const markup = renderToStaticMarkup(createElement(ReportVersionHistory, { history, currentVersion: 2, onSelect: () => {}, message: "", canPublish: true }));
    expect(markup).toContain("4 componentes no escopo · Não revisado");
    expect(markup).toContain("Escopo não definido · Falha na análise");
    expect(markup).toContain("02/10/2026");
    expect(markup).toContain("Versão anterior");
  });
});

function requirement(key: string, section: string, label: string, instructions: string | null = null, analysisMode = "COMPARE_ORIGIN_CURRENT", analysisStatus = "COMPLETED") {
  return { key: `video:${key}`, section, label, instructions, noRelevantChange: null, analysisMode, analysisStatus };
}

function evidence(id: string, requirementKey: string, role: string, url: string, evidenceKind = "PHOTO", sourceMediaId?: string, timestampMs?: number) {
  return { id, requirementKey, role, description: "Imagem da evidência", capturedAt: null, flags: [], availability: "AVAILABLE", url, evidenceKind, sourceMediaId, timestampMs, frameId: id, ptsMicros: timestampMs === undefined ? null : String(timestampMs * 1000), label: null, sourceAvailability: "AVAILABLE" };
}

function reportWith(requirements: ReturnType<typeof requirement>[], nodes: ReturnType<typeof evidence>[], findings: Array<{ title: string; description: string; severity: string; recommendedAction: string; evidenceIds: string[] }>) {
  return baseReport({ requirements, evidence: { nodes, pageInfo: { endCursor: null, hasNextPage: false } }, findings });
}

function baseReport(value: { requirements: ReturnType<typeof requirement>[]; evidence: { nodes: ReturnType<typeof evidence>[]; pageInfo: { endCursor: null; hasNextPage: boolean } }; findings: Array<{ title: string; description: string; severity: string; recommendedAction: string; evidenceIds: string[] }> }) {
  return {
    id: "report-1", version: 1, classification: "ATTENTION", advisory: "Esta triagem interna descreve alterações observadas.",
    context: { asset: { name: "Imóvel QA", address: "Rua QA", externalKey: "QA-1" }, participant: { name: "QA" }, template: { name: "Padrão", version: 1 }, inspection: { stageLabel: "Entrada" } },
    requirements: value.requirements, evidence: value.evidence, findings: value.findings,
  } as unknown as ComponentProps<typeof ReportVisual>["report"];
}
