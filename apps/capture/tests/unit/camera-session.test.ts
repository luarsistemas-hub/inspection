import { afterEach, describe, expect, it, vi } from "vitest";
import { canOpenCamera, openCamera, releaseCamera } from "@/pwa/camera-session";

describe("camera session", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("opens a stream with video only and releases its tracks on close", async () => {
    const stop = vi.fn();
    const stream = { getTracks: () => [{ stop }] } as unknown as MediaStream;
    const getUserMedia = vi.fn().mockResolvedValue(stream);
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia } });

    expect(canOpenCamera()).toBe(true);
    await expect(openCamera()).resolves.toBe(stream);
    expect(getUserMedia).toHaveBeenCalledWith({ audio: false, video: { facingMode: { ideal: "environment" }, width: { ideal: 1920 }, height: { ideal: 1080 }, frameRate: { ideal: 30 } } });
    releaseCamera(stream);
    expect(stop).toHaveBeenCalledOnce();
  });

  it("reports missing camera support without calling a recorder", async () => {
    vi.stubGlobal("navigator", { mediaDevices: undefined });
    expect(canOpenCamera()).toBe(false);
    await expect(openCamera()).rejects.toThrow(/não oferece acesso à câmera/);
  });
});
