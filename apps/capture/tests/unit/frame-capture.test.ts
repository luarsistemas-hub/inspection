import { afterEach, describe, expect, it, vi } from "vitest";
import { captureFrame } from "@/pwa/frame-capture";

const frame = () => ({ videoWidth: 1280, videoHeight: 720 } as HTMLVideoElement);

describe("captureFrame", () => {
  afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

  it("draws the exact video dimensions on the conventional canvas", async () => {
    const drawImage = vi.fn();
    const toBlob = vi.fn((callback: BlobCallback) => callback(new Blob(["jpeg"], { type: "image/jpeg" })));
    const canvas = { width: 0, height: 0, getContext: vi.fn(() => ({ drawImage })), toBlob };
    vi.stubGlobal("OffscreenCanvas", undefined);
    vi.spyOn(document, "createElement").mockReturnValue(canvas as unknown as HTMLCanvasElement);
    const file = await captureFrame(frame(), { now: () => new Date(2026, 9, 27, 9, 8, 7) });
    expect(drawImage).toHaveBeenCalledWith(expect.objectContaining({ videoWidth: 1280, videoHeight: 720 }), 0, 0, 1280, 720);
    expect(file.name).toBe("camera-20261027-090807.jpg");
    expect(canvas.width).toBe(0);
    expect(canvas.height).toBe(0);
  });

  it("rejects when the video frame has no dimensions", async () => {
    await expect(captureFrame({ videoWidth: 0, videoHeight: 720 } as HTMLVideoElement)).rejects.toThrow("camera-frame-unavailable");
  });

  it("falls back to the conventional canvas when the offscreen context is null", async () => {
    const offscreen = { getContext: vi.fn(() => null) };
    const drawImage = vi.fn();
    const canvas = { width: 0, height: 0, getContext: () => ({ drawImage }), toBlob: (cb: BlobCallback) => cb(new Blob(["x"])) };
    vi.stubGlobal("OffscreenCanvas", vi.fn(function () { return offscreen; }));
    vi.spyOn(document, "createElement").mockReturnValue(canvas as unknown as HTMLCanvasElement);
    await expect(captureFrame(frame())).resolves.toBeInstanceOf(File);
    expect(offscreen.getContext).toHaveBeenCalledWith("2d");
    expect(drawImage).toHaveBeenCalledOnce();
  });

  it("falls back to the conventional canvas when convertToBlob rejects", async () => {
    const offscreen = { getContext: () => ({ drawImage: vi.fn() }), convertToBlob: vi.fn().mockRejectedValue(new Error("no")) };
    const canvas = { width: 0, height: 0, getContext: () => ({ drawImage: vi.fn() }), toBlob: (cb: BlobCallback) => cb(new Blob(["x"])) };
    vi.stubGlobal("OffscreenCanvas", vi.fn(function () { return offscreen; }));
    vi.spyOn(document, "createElement").mockReturnValue(canvas as unknown as HTMLCanvasElement);
    await expect(captureFrame(frame())).resolves.toBeInstanceOf(File);
    expect(offscreen.convertToBlob).toHaveBeenCalledWith({ type: "image/jpeg", quality: 0.92 });
  });

  it("rejects when all canvas paths fail or return a null blob", async () => {
    const canvas = { width: 0, height: 0, getContext: () => ({ drawImage: vi.fn() }), toBlob: (cb: BlobCallback) => cb(null) };
    vi.stubGlobal("OffscreenCanvas", undefined);
    vi.spyOn(document, "createElement").mockReturnValue(canvas as unknown as HTMLCanvasElement);
    await expect(captureFrame(frame())).rejects.toThrow("camera-frame-unavailable");
  });
});
