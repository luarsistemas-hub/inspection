import { afterEach, describe, expect, it, vi } from "vitest";
import { CameraInsecureContextError, CameraUnsupportedError, canOpenCamera, classifyCameraError, isInAppBrowser, openCamera, openPhotoCamera, photoConstraints, releaseCamera } from "@/pwa/camera-session";

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

  it("retries photo constraints with video constraints only when overconstrained", async () => {
    const stream = {} as MediaStream;
    const overconstrained = Object.assign(new Error("constraint"), { name: "OverconstrainedError" });
    const getUserMedia = vi.fn().mockRejectedValueOnce(overconstrained).mockResolvedValueOnce(stream);
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia } });
    vi.stubGlobal("isSecureContext", true);
    await expect(openPhotoCamera()).resolves.toBe(stream);
    expect(getUserMedia).toHaveBeenNthCalledWith(1, photoConstraints);
    expect(getUserMedia).toHaveBeenNthCalledWith(2, { audio: false, video: { facingMode: { ideal: "environment" }, width: { ideal: 1920 }, height: { ideal: 1080 }, frameRate: { ideal: 30 } } });
  });

  it("propagates photo permission errors unchanged", async () => {
    const denied = Object.assign(new Error("denied"), { name: "NotAllowedError" });
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia: vi.fn().mockRejectedValue(denied) } });
    vi.stubGlobal("isSecureContext", true);
    await expect(openPhotoCamera()).rejects.toBe(denied);
  });

  it("classifies camera errors by browser error name", () => {
    const cases: Array<[string, ReturnType<typeof classifyCameraError>]> = [
      ["NotAllowedError", "denied"], ["PermissionDeniedError", "denied"],
      ["NotReadableError", "busy"], ["TrackStartError", "busy"], ["AbortError", "busy"],
      ["NotFoundError", "unavailable"], ["DevicesNotFoundError", "unavailable"], ["OverconstrainedError", "unavailable"],
      ["SecurityError", "insecure"], ["TypeError", "unknown"]
    ];
    for (const [name, failure] of cases) expect(classifyCameraError({ name })).toBe(failure);
    expect(classifyCameraError(new CameraUnsupportedError())).toBe("unsupported");
    expect(classifyCameraError(new CameraInsecureContextError())).toBe("insecure");
  });

  it("checks insecure context before unsupported camera access", async () => {
    vi.stubGlobal("navigator", { mediaDevices: undefined });
    vi.stubGlobal("isSecureContext", false);
    await expect(openCamera()).rejects.toBeInstanceOf(CameraInsecureContextError);
    await expect(openPhotoCamera()).rejects.toBeInstanceOf(CameraInsecureContextError);
    expect(classifyCameraError(new CameraInsecureContextError())).toBe("insecure");
  });

  it("recognizes common in-app browsers and ordinary browsers", () => {
    for (const userAgent of ["Instagram 300", "WhatsApp/2.0", "Mozilla/5.0; wv) Android", "GSA/123 iPhone"]) expect(isInAppBrowser(userAgent)).toBe(true);
    for (const userAgent of ["iPhone Safari", "Chrome Android", "Firefox Android"]) expect(isInAppBrowser(userAgent)).toBe(false);
  });
});
