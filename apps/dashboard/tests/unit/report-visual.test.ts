import { describe, expect, it } from "vitest";
import { act, createElement, type ComponentProps } from "react";
import { createRoot } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { ReportVisual, unmatchedReportEvidence } from "@/features/dashboard/report-visual";
import { buildReportRooms } from "@/features/dashboard/report-room-model";

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
