import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ReportVisual, VideoEvidencePlayer } from "@/features/dashboard/report-visual";

const prepareSession = vi.hoisted(() => vi.fn());
vi.mock("@/auth/video-playback-session", () => ({ prepareVideoPlaybackSession: prepareSession }));

const item = {
  id: "evidence-001",
  requirementKey: "kitchen",
  role: "CURRENT",
  flags: [],
  availability: "AVAILABLE",
  evidenceKind: "VIDEO_FRAME",
  frameId: "frame-12345678",
  timestampMs: 70_000,
  ptsMicros: "70000000",
  sourceMediaId: "source-001",
  component: "sink",
};

describe("VideoEvidencePlayer", () => {
  afterEach(() => { prepareSession.mockReset(); document.body.replaceChildren(); });
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

  it("carrega o BFF somente depois da ativação e posiciona o vídeo no tempo do frame", async () => {
    prepareSession.mockResolvedValue(undefined);
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    await act(async () => { root.render(createElement(VideoEvidencePlayer, { item })); });
    expect(host.querySelector("video")).toBeNull();
    expect(prepareSession).not.toHaveBeenCalled();

    await act(async () => { host.querySelector("button")?.dispatchEvent(new MouseEvent("click", { bubbles: true })); });
    const video = host.querySelector("video");
    expect(prepareSession).toHaveBeenCalledOnce();
    expect(video?.getAttribute("src")).toBe("/api/media/videos/source-001/content");
    expect(video?.getAttribute("preload")).toBe("metadata");
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    await act(async () => { video?.dispatchEvent(new Event("loadedmetadata", { bubbles: true })); });
    expect(video?.currentTime).toBe(70);
    expect(host.textContent).toContain("Ir para 01:10");
    await act(async () => { root.unmount(); });
  });

  it("mantém o frame disponível e não monta vídeo quando a fonte expirou", async () => {
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    await act(async () => { root.render(createElement(VideoEvidencePlayer, { item: { ...item, sourceAvailability: "EXPIRED" } })); });
    await act(async () => { host.querySelector("button")?.dispatchEvent(new MouseEvent("click", { bubbles: true })); });
    expect(host.textContent).toContain("Vídeo original expirado");
    expect(host.querySelector("video")).toBeNull();
    expect(prepareSession).not.toHaveBeenCalled();
    await act(async () => { root.unmount(); });
  });

  it("não inicia reprodução quando o timestamp é inválido ou excede a duração", async () => {
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    await act(async () => { root.render(createElement(VideoEvidencePlayer, { item: { ...item, timestampMs: -1 } })); });
    await act(async () => { host.querySelector("button")?.dispatchEvent(new MouseEvent("click", { bubbles: true })); });
    expect(host.textContent).toContain("Evidência de vídeo indisponível neste timestamp");
    expect(host.querySelector("video")).toBeNull();
    expect(prepareSession).not.toHaveBeenCalled();
    await act(async () => { root.unmount(); });
  });

  it("preserva o frame quando o timestamp ultrapassa a duração do vídeo", async () => {
    prepareSession.mockResolvedValue(undefined);
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    await act(async () => { root.render(createElement(VideoEvidencePlayer, { item })); });
    await act(async () => { host.querySelector("button")?.dispatchEvent(new MouseEvent("click", { bubbles: true })); });
    const video = host.querySelector("video");
    Object.defineProperty(video, "duration", { configurable: true, value: 60 });
    await act(async () => { video?.dispatchEvent(new Event("loadedmetadata", { bubbles: true })); });
    expect(host.textContent).toContain("Evidência de vídeo indisponível neste timestamp");
    expect(video?.currentTime).toBe(0);
    await act(async () => { root.unmount(); });
  });

  it("IT-279 mostra legendas independentes de origem 00:10 e atual 01:10 no achado da pia", async () => {
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    await act(async () => { root.render(createElement(ReportVisual, { report: reportWithEvidence([
      { ...item, id: "origin-frame", role: "REFERENCE", timestampMs: 10_000, ptsMicros: "10000000", description: "Frame origem" },
      { ...item, id: "current-frame", role: "CURRENT", timestampMs: 70_000, ptsMicros: "70000000", description: "Frame atual" },
    ]), onDownload: () => {} })); });
    const text = host.textContent ?? "";
    expect(text).toContain("ORIGEM");
    expect(text).toContain("ATUAL");
    expect(text).toContain("pia · 00:10");
    expect(text).toContain("pia · 01:10");
    await act(async () => { root.unmount(); });
  });

  it.each(["IT-281", "IT-285"])("%s mantém F11 legível quando C1 expirou", async () => {
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    await act(async () => { root.render(createElement(ReportVisual, { report: reportWithEvidence([
      { ...item, id: "F11", frameId: "F11-frame", sourceAvailability: "EXPIRED", description: "Frame F11 retido" },
    ]), onDownload: () => {} })); });
    expect(host.textContent).toContain("Frame F11 retido");
    expect(host.textContent).toContain("Vídeo original expirado; frame preservado.");
    expect(host.querySelector("video")).toBeNull();
    expect(prepareSession).not.toHaveBeenCalled();
    await act(async () => { root.unmount(); });
  });

  it("IT-282 mantém imagens, achados e limitações após erro de codec", async () => {
    prepareSession.mockResolvedValue(undefined);
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    await act(async () => { root.render(createElement(ReportVisual, { report: reportWithEvidence([
      { ...item, url: "/frame-1.png", description: "Frame da pia" },
    ], [{ title: "Vazamento", description: "Achado visível", severity: "HIGH", recommendedAction: "Verificar", evidenceIds: [item.id] }]), onDownload: () => {} })); });
    await act(async () => { host.querySelector(".report-video-activate")?.dispatchEvent(new MouseEvent("click", { bubbles: true })); });
    const video = host.querySelector("video");
    expect(video).not.toBeNull();
    await act(async () => { video?.dispatchEvent(new Event("error", { bubbles: true })); });
    expect(host.querySelector("img")?.getAttribute("src")).toBe("/frame-1.png");
    expect(host.textContent).toContain("Frame da pia");
    expect(host.textContent).toContain("Vazamento");
    expect(host.textContent).toContain("Limitação: reprodução depende do codec do navegador.");
    expect(host.textContent).toContain("Este navegador não reproduz o vídeo.");
    await act(async () => { root.unmount(); });
  });

  it("IT-283 usa o PTS individual de F01 e F11 sem sincronizar os players", async () => {
    prepareSession.mockResolvedValue(undefined);
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    await act(async () => { root.render(createElement(ReportVisual, { report: reportWithEvidence([
      { ...item, id: "F01", frameId: "F01", role: "REFERENCE", timestampMs: 10_000, ptsMicros: "10000000" },
      { ...item, id: "F11", frameId: "F11", role: "CURRENT", timestampMs: 70_000, ptsMicros: "70000000" },
    ]), onDownload: () => {} })); });
    const activationButtons = host.querySelectorAll<HTMLButtonElement>(".report-video-activate");
    expect(activationButtons).toHaveLength(2);
    await act(async () => { activationButtons[0].click(); activationButtons[1].click(); });
    const videos = host.querySelectorAll("video");
    expect(videos).toHaveLength(2);
    for (const video of videos) {
      Object.defineProperty(video, "duration", { configurable: true, value: 120 });
      await act(async () => { video.dispatchEvent(new Event("loadedmetadata", { bubbles: true })); });
    }
    expect(videos[0].currentTime).toBe(10);
    expect(videos[1].currentTime).toBe(70);
    expect(videos[0].getAttribute("src")).toBe("/api/media/videos/source-001/content");
    expect(videos[1].getAttribute("src")).toBe("/api/media/videos/source-001/content");
    await act(async () => { root.unmount(); });
  });

  it("IT-293 não cria stream para nenhum dos 200 achados antes da ativação", async () => {
    prepareSession.mockResolvedValue(undefined);
    const evidence = Array.from({ length: 200 }, (_, index) => ({ ...item, id: `evidence-${index}`, frameId: `frame-${index}` }));
    const host = document.createElement("div");
    document.body.append(host);
    const root = createRoot(host);
    await act(async () => { root.render(createElement(ReportVisual, { report: reportWithEvidence(evidence), onDownload: () => {} })); });
    expect(host.querySelectorAll(".report-video-activate")).toHaveLength(200);
    expect(host.querySelectorAll("video")).toHaveLength(0);
    expect(host.querySelectorAll('video[src*="/content"]')).toHaveLength(0);
    expect(prepareSession).not.toHaveBeenCalled();
    await act(async () => { host.querySelector<HTMLButtonElement>(".report-video-activate")?.click(); });
    expect(host.querySelectorAll("video")).toHaveLength(1);
    expect(host.querySelectorAll('video[src$="/content"]')).toHaveLength(1);
    await act(async () => { root.unmount(); });
  });
});

function reportWithEvidence(evidence: Array<Record<string, unknown>>, findings: Array<Record<string, unknown>> = []) {
  return {
    id: "report-video", version: 1, classification: "ATTENTION", advisory: "Limitação: reprodução depende do codec do navegador.",
    context: { asset: { name: "Imóvel QA", address: "Rua QA", externalKey: "QA-1" }, participant: { name: "QA" }, template: { name: "Padrão", version: 1 }, inspection: { stageLabel: "Entrada" } },
    requirements: [{ key: "video:kitchen", section: "Cozinha", label: "Cozinha", instructions: null, noRelevantChange: null, analysisMode: "COMPLETED", analysisStatus: "COMPLETED" }],
    evidence: { nodes: evidence.map((entry) => ({ ...entry, requirementKey: "video:kitchen", flags: [], availability: "AVAILABLE", sourceMediaId: "source-001", component: "pia" })), pageInfo: { endCursor: null, hasNextPage: false } },
    findings: findings.map((finding) => ({ ...finding, evidenceIds: finding.evidenceIds ?? [] })),
  } as unknown as Parameters<typeof ReportVisual>[0]["report"];
}
