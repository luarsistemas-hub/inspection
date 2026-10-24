import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ReportVisual, VideoEvidencePlayer } from "@/features/dashboard/report-visual";

const prepareSession = vi.hoisted(() => vi.fn());
vi.mock("@/auth/video-playback-session", () => ({ prepareVideoPlaybackSession: prepareSession }));

const item = {
  id: "evidence-001", snapshotId: "snapshot-001", requirementKey: "kitchen", role: "CURRENT", flags: [], availability: "AVAILABLE",
  evidenceKind: "VIDEO_FRAME", frameId: "frame-12345678", timestampMs: 70_000, ptsMicros: "70000000", sourceMediaId: "source-001", component: "sink",
};

describe("VideoEvidencePlayer", () => {
  beforeEach(() => { vi.spyOn(HTMLMediaElement.prototype, "play").mockResolvedValue(undefined); });
  afterEach(() => { vi.restoreAllMocks(); prepareSession.mockReset(); document.body.replaceChildren(); });
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

  it("carrega o BFF somente depois da ativação e posiciona o vídeo no tempo do frame", async () => {
    prepareSession.mockResolvedValue(undefined);
    const host = document.createElement("div"); document.body.append(host); const root = createRoot(host);
    await act(async () => { root.render(createElement(VideoEvidencePlayer, { item })); });
    expect(host.querySelector("video")).toBeNull(); expect(prepareSession).not.toHaveBeenCalled();
    await act(async () => { host.querySelector<HTMLButtonElement>("button")?.click(); });
    const video = host.querySelector("video");
    expect(prepareSession).toHaveBeenCalledOnce();
    expect(video?.getAttribute("src")).toBe("/api/media/videos/source-001/content?snapshotId=snapshot-001");
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    await act(async () => { video?.dispatchEvent(new Event("loadedmetadata", { bubbles: true })); });
    expect(video?.currentTime).toBe(70); expect(video?.play).toHaveBeenCalledOnce();
    await act(async () => { root.unmount(); });
  });

  it("mantém o erro de sessão visível e permite tentar novamente sem recarregar o laudo", async () => {
    prepareSession.mockRejectedValueOnce(new Error("403")).mockResolvedValueOnce(undefined);
    const onMediaError = vi.fn(); const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => { root.render(createElement(VideoEvidencePlayer, { item: { ...item, url: "/preview.png" }, onMediaError })); });
    await act(async () => { host.querySelector<HTMLButtonElement>("button")?.click(); });
    expect(host.textContent).toContain("tentar novamente"); expect(onMediaError).not.toHaveBeenCalled();
    await act(async () => { host.querySelector<HTMLButtonElement>("button")?.click(); });
    expect(host.querySelector("video")).not.toBeNull(); expect(prepareSession).toHaveBeenCalledTimes(2);
    await act(async () => { root.unmount(); });
  });

  it("oferece os controles nativos quando o navegador bloqueia play", async () => {
    prepareSession.mockResolvedValue(undefined); vi.mocked(HTMLMediaElement.prototype.play).mockRejectedValue(new DOMException("Blocked", "NotAllowedError"));
    const host = document.createElement("div"); const root = createRoot(host);
    await act(async () => { root.render(createElement(VideoEvidencePlayer, { item })); });
    await act(async () => { host.querySelector<HTMLButtonElement>("button")?.click(); });
    const video = host.querySelector("video")!; Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    await act(async () => { video.dispatchEvent(new Event("loadedmetadata")); });
    expect(video.controls).toBe(true); expect(host.textContent).toContain("Use o controle de reprodução");
    await act(async () => { root.unmount(); });
  });

  it("mantém o frame disponível e não monta vídeo quando a fonte expirou", async () => {
    const host = document.createElement("div"); document.body.append(host); const root = createRoot(host);
    await act(async () => { root.render(createElement(VideoEvidencePlayer, { item: { ...item, sourceAvailability: "EXPIRED" } })); });
    await act(async () => { host.querySelector<HTMLButtonElement>("button")?.click(); });
    expect(host.textContent).toContain("Vídeo original expirado"); expect(host.querySelector("video")).toBeNull(); expect(prepareSession).not.toHaveBeenCalled();
    await act(async () => { root.unmount(); });
  });

  it("não inicia reprodução quando o timestamp é inválido ou excede a duração", async () => {
    const host = document.createElement("div"); document.body.append(host); const root = createRoot(host);
    await act(async () => { root.render(createElement(VideoEvidencePlayer, { item: { ...item, timestampMs: -1 } })); });
    await act(async () => { host.querySelector<HTMLButtonElement>("button")?.click(); });
    expect(host.textContent).toContain("Evidência de vídeo indisponível neste timestamp"); expect(host.querySelector("video")).toBeNull(); expect(prepareSession).not.toHaveBeenCalled();
    await act(async () => { root.unmount(); });
  });

  it("abre um único modal para o par e começa o vídeo atual no ponto da evidência", async () => {
    prepareSession.mockResolvedValue(undefined);
    const host = document.createElement("div"); document.body.append(host); const root = createRoot(host);
    await act(async () => { root.render(createElement(ReportVisual, { report: reportWithEvidence([
      { ...item, id: "origin-frame", role: "REFERENCE", timestampMs: 10_000, sourceMediaId: "origin-video", description: "Origem", url: "/origin.jpg" },
      { ...item, id: "current-frame", role: "CURRENT", timestampMs: 70_000, sourceMediaId: "current-video", description: "Atual", url: "/current.jpg" },
    ]), onDownload: () => {} })); });
    expect(host.querySelectorAll("img")).toHaveLength(0); expect(host.querySelectorAll(".report-pair-play")).toHaveLength(0);
    await act(async () => { host.querySelector<HTMLButtonElement>(".report-room-toggle")?.click(); });
    await act(async () => { host.querySelector<HTMLButtonElement>(".report-evidence-toggle")?.click(); });
    expect(host.querySelectorAll(".report-pair-play")).toHaveLength(1);
    await act(async () => { host.querySelector<HTMLButtonElement>(".report-pair-play")?.click(); });
    const video = document.body.querySelector("video");
    expect(prepareSession).toHaveBeenCalledOnce(); expect(video?.getAttribute("src")).toContain("current-video");
    Object.defineProperty(video, "duration", { configurable: true, value: 120 });
    await act(async () => { video?.dispatchEvent(new Event("loadedmetadata", { bubbles: true })); });
    expect(video?.currentTime).toBe(70); expect(video?.play).toHaveBeenCalledOnce();
    await act(async () => { [...document.body.querySelectorAll<HTMLButtonElement>(".report-video-source-switch button")].find((button) => button.textContent === "Origem")?.click(); });
    expect(document.body.querySelector("video")?.getAttribute("src")).toContain("origin-video");
    await act(async () => { document.body.querySelector<HTMLButtonElement>('button[aria-label="Fechar vídeo"]')?.click(); });
    expect(document.body.querySelector("video")).toBeNull();
    await act(async () => { root.unmount(); });
  });

  it("preserva o frame quando o vídeo expira e comunica falha de codec no modal", async () => {
    const expiredHost = document.createElement("div"); document.body.append(expiredHost); const expiredRoot = createRoot(expiredHost);
    await act(async () => { expiredRoot.render(createElement(ReportVisual, { report: reportWithEvidence([{ ...item, sourceAvailability: "EXPIRED", description: "Frame retido", url: "/frame.jpg" }]), onDownload: () => {} })); });
    await act(async () => { expiredHost.querySelector<HTMLButtonElement>(".report-room-toggle")?.click(); });
    await act(async () => { expiredHost.querySelector<HTMLButtonElement>(".report-evidence-toggle")?.click(); });
    expect(expiredHost.textContent).toContain("Frame retido");
    await act(async () => { expiredHost.querySelector<HTMLButtonElement>(".report-pair-play")?.click(); });
    expect(document.body.textContent).toContain("Vídeo indisponível neste momento"); expect(expiredHost.querySelector("img")?.getAttribute("src")).toBe("/frame.jpg");
    await act(async () => { expiredRoot.unmount(); });

    prepareSession.mockResolvedValue(undefined);
    const host = document.createElement("div"); document.body.append(host); const root = createRoot(host);
    await act(async () => { root.render(createElement(ReportVisual, { report: reportWithEvidence([{ ...item, url: "/frame-1.jpg", description: "Frame da pia" }], [{ title: "Vazamento", description: "Achado visível", severity: "HIGH", recommendedAction: "Verificar", evidenceIds: [item.id] }]), onDownload: () => {} })); });
    await act(async () => { host.querySelector<HTMLButtonElement>(".report-room-toggle")?.click(); });
    await act(async () => { host.querySelector<HTMLButtonElement>(".report-pair-play")?.click(); });
    const video = document.body.querySelector("video"); expect(video).not.toBeNull();
    await act(async () => { video?.dispatchEvent(new Event("error", { bubbles: true })); });
    expect(host.textContent).toContain("Frame da pia"); expect(host.textContent).toContain("Vazamento");
    expect(document.body.textContent).toContain("não conseguiu reproduzir o vídeo");
    await act(async () => { root.unmount(); });
  });
});

function reportWithEvidence(evidence: Array<Record<string, unknown>>, findings: Array<Record<string, unknown>> = []) {
  return {
    id: "report-video", version: 1, classification: "ATTENTION", advisory: "Limitação: reprodução depende do codec do navegador.",
    context: { asset: { name: "Imóvel QA", address: "Rua QA", externalKey: "QA-1" }, participant: { name: "QA" }, template: { name: "Padrão", version: 1 }, inspection: { stageLabel: "Entrada" } },
    requirements: [{ key: "video:kitchen", section: "Cozinha", label: "Cozinha", instructions: null, noRelevantChange: null, analysisMode: "COMPLETED", analysisStatus: "COMPLETED" }],
    evidence: { nodes: evidence.map((entry) => ({ flags: [], availability: "AVAILABLE", sourceMediaId: "source-001", component: "pia", ...entry, requirementKey: "video:kitchen" })), pageInfo: { endCursor: null, hasNextPage: false } },
    findings: findings.map((finding) => ({ ...finding, evidenceIds: finding.evidenceIds ?? [] })),
  } as unknown as Parameters<typeof ReportVisual>[0]["report"];
}
