import { afterEach, describe, expect, it, vi } from "vitest";
import { captureFallbackProfile, prepareCaptureImage } from "@/pwa/prepare-image";

function mockCanvas(blob: Blob | null) {
  const drawImage = vi.fn();
  const fillRect = vi.fn();
  vi.spyOn(document, "createElement").mockImplementation(((name: string) => {
    if (name !== "canvas") return document.createElementNS("http://www.w3.org/1999/xhtml", name) as HTMLElement;
    return {
      width: 0,
      height: 0,
      getContext: () => ({ fillStyle: "", fillRect, drawImage }),
      toBlob: (callback: BlobCallback, type: string) => callback(blob && type === blob.type ? blob : null),
    } as unknown as HTMLCanvasElement;
  }) as typeof document.createElement);
  return { drawImage, fillRect };
}

afterEach(() => vi.restoreAllMocks());

describe("prepareCaptureImage", () => {
  it("resizes, preserves aspect ratio and returns the actual WebP output", async () => {
    const { drawImage, fillRect } = mockCanvas(new Blob([new Uint8Array(10)], { type: "image/webp" }));
    const close = vi.fn();
    const decode = vi.fn().mockResolvedValue({ width: 4096, height: 2048, close });
    vi.stubGlobal("createImageBitmap", decode);
    const source = new File([new Uint8Array(100)], "room.jpg", { type: "image/jpeg" });

    const prepared = await prepareCaptureImage(source);

    expect(prepared.file.type).toBe("image/webp");
    expect(prepared.file.name).toBe("room.webp");
    expect(prepared.profile).toBe("capture-2048-webp85-v1");
    expect(prepared.changed).toBe(true);
    expect(decode).toHaveBeenCalledWith(source, { imageOrientation: "from-image" });
    expect(drawImage).toHaveBeenCalledWith(expect.anything(), 0, 0, 2048, 1024);
    expect(fillRect).toHaveBeenCalledWith(0, 0, 2048, 1024);
    expect(close).toHaveBeenCalledOnce();
  });

  it("falls back to original bytes when browser decoding fails", async () => {
    vi.stubGlobal("createImageBitmap", vi.fn().mockRejectedValue(new Error("unsupported HEIC")));
    const source = new File([new Uint8Array(12)], "photo.heic", { type: "image/heic" });

    const prepared = await prepareCaptureImage(source);

    expect(prepared.file).toBe(source);
    expect(prepared.profile).toBe(captureFallbackProfile);
    expect(prepared.changed).toBe(false);
  });

  it("keeps smaller original bytes if re-encoding does not resize", async () => {
    mockCanvas(new Blob([new Uint8Array(100)], { type: "image/webp" }));
    vi.stubGlobal("createImageBitmap", vi.fn().mockResolvedValue({ width: 800, height: 600, close: vi.fn() }));
    const source = new File([new Uint8Array(10)], "small.jpg", { type: "image/jpeg" });

    const prepared = await prepareCaptureImage(source);

    expect(prepared.file).toBe(source);
    expect(prepared.changed).toBe(false);
  });

  it("uses JPEG when WebP encoding is unsupported", async () => {
    mockCanvas(new Blob([new Uint8Array(10)], { type: "image/jpeg" }));
    vi.stubGlobal("createImageBitmap", vi.fn().mockResolvedValue({ width: 1200, height: 900, close: vi.fn() }));
    const source = new File([new Uint8Array(100)], "room.png", { type: "image/png" });

    const prepared = await prepareCaptureImage(source);

    expect(prepared.file.type).toBe("image/jpeg");
    expect(prepared.file.name).toBe("room.jpg");
    expect(prepared.profile).toBe("capture-2048-jpeg85-v1");
  });
});
