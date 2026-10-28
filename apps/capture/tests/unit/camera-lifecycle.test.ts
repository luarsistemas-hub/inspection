import { afterEach, describe, expect, it, vi } from "vitest";
import { attachPreview, openCameraCancellable, playPreview } from "@/pwa/camera-session";
import { captureErrorMessage, visibleDrafts } from "@/pwa/video-capture-state";
import { acquireScreenWakeLock } from "@/pwa/wake-lock";
import type { VideoDraft } from "@/pwa/video-drafts";

const streamWith = (stop = vi.fn()) => ({ getTracks: () => [{ stop }, { stop }] }) as unknown as MediaStream;

describe("camera lifecycle helpers", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("stops every track of a permission granted after the open was cancelled", async () => {
    const stop = vi.fn();
    let grant!: (stream: MediaStream) => void;
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: () => new Promise<MediaStream>((resolve) => { grant = resolve; }) } });
    let cancelled = false;
    const opening = openCameraCancellable(() => cancelled);
    cancelled = true;
    grant(streamWith(stop));
    await expect(opening).resolves.toBeUndefined();
    expect(stop).toHaveBeenCalledTimes(2);
  });

  it("returns the stream untouched when the open was not cancelled", async () => {
    const stop = vi.fn();
    const stream = streamWith(stop);
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: async () => stream } });
    await expect(openCameraCancellable(() => false)).resolves.toBe(stream);
    expect(stop).not.toHaveBeenCalled();
  });

  it("reports a blocked preview when play() is refused and attaches the same stream", async () => {
    const stream = streamWith();
    const blocked = { srcObject: null as MediaProvider | null, play: vi.fn().mockRejectedValue(new DOMException("blocked", "NotAllowedError")) };
    await expect(attachPreview(blocked, stream)).resolves.toBe("blocked");
    expect(blocked.srcObject).toBe(stream);
    await expect(playPreview({ play: async () => undefined })).resolves.toBe("playing");
  });

  it("maps camera failures to recovery messages", () => {
    expect(captureErrorMessage(new DOMException("x", "NotAllowedError"))).toMatch(/Câmera bloqueada/);
    expect(captureErrorMessage(new DOMException("x", "NotReadableError"))).toMatch(/ocupada ou indisponível/);
    expect(captureErrorMessage(new DOMException("x", "NotFoundError"))).toBe("Nenhuma câmera disponível neste dispositivo.");
    expect(captureErrorMessage(new DOMException("x", "OverconstrainedError"))).toBe("Nenhuma câmera disponível neste dispositivo.");
    expect(captureErrorMessage(new DOMException("x", "QuotaExceededError"))).toMatch(/sem espaço/);
    expect(captureErrorMessage(new Error("INTERNAL_SERVER_ERROR: recorder failed"))).toMatch(/Revise a gravação/);
    expect(captureErrorMessage(new Error("O vídeo excede o limite. Grave novamente."))).toBe("O vídeo excede o limite. Grave novamente.");
    expect(captureErrorMessage(new Error("environment requirement is required"))).toContain("O ambiente escolhido não está disponível");
  });

  it("requests and releases the screen wake lock when available", async () => {
    const release = vi.fn().mockResolvedValue(undefined);
    const request = vi.fn().mockResolvedValue({ release });
    vi.stubGlobal("navigator", { wakeLock: { request } });
    const releaseLock = await acquireScreenWakeLock();
    expect(request).toHaveBeenCalledWith("screen");
    await releaseLock();
    expect(release).toHaveBeenCalledOnce();
  });

  it("keeps recording usable when the wake lock is refused or missing", async () => {
    vi.stubGlobal("navigator", { wakeLock: { request: vi.fn().mockRejectedValue(new DOMException("denied", "NotAllowedError")) } });
    await expect((await acquireScreenWakeLock())()).resolves.toBeUndefined();
    vi.stubGlobal("navigator", {});
    await expect((await acquireScreenWakeLock())()).resolves.toBeUndefined();
  });

  it("hides the local draft list while the camera is in use", () => {
    const items = [{ id: "a", state: "INCOMPLETE" } as VideoDraft];
    for (const phase of ["opening", "ready", "starting", "recording", "finalizing"]) expect(visibleDrafts(items, phase)).toEqual([]);
    expect(visibleDrafts(items, "idle")).toBe(items);
  });
});
