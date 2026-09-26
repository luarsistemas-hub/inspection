export const captureFallbackProfile = "capture-original-fallback-v1";

export type PreparedCaptureImage = { file: File; profile: string; changed: boolean };

const maxDimension = 2048;

function canvasBlob(canvas: HTMLCanvasElement, type: string, quality: number): Promise<Blob | null> {
  return new Promise((resolve) => canvas.toBlob(resolve, type, quality));
}

function outputType(blob: Blob): "image/webp" | "image/jpeg" | undefined {
  if (blob.type === "image/webp" || blob.type === "image/jpeg") return blob.type;
  return undefined;
}

function outputName(name: string, type: "image/webp" | "image/jpeg"): string {
  const stem = name.replace(/\.[^.]+$/, "") || "capture";
  return `${stem}.${type === "image/webp" ? "webp" : "jpg"}`;
}

/** Prepares a capture while preserving the original if browser decoding fails. */
export async function prepareCaptureImage(file: File): Promise<PreparedCaptureImage> {
  let bitmap: ImageBitmap | undefined;
  try {
    if (typeof createImageBitmap !== "function") throw new Error("Image decoding is unavailable");
    bitmap = await createImageBitmap(file, { imageOrientation: "from-image" });
    if (!bitmap.width || !bitmap.height) throw new Error("Invalid image dimensions");

    const scale = Math.min(1, maxDimension / Math.max(bitmap.width, bitmap.height));
    const width = Math.max(1, Math.round(bitmap.width * scale));
    const height = Math.max(1, Math.round(bitmap.height * scale));
    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    const context = canvas.getContext("2d");
    if (!context) throw new Error("Canvas is unavailable");
    context.fillStyle = "#fff";
    context.fillRect(0, 0, width, height);
    context.drawImage(bitmap, 0, 0, width, height);

    let blob = await canvasBlob(canvas, "image/webp", 0.85);
    let type = blob && outputType(blob);
    if (!blob || !type) {
      blob = await canvasBlob(canvas, "image/jpeg", 0.85);
      type = blob && outputType(blob);
    }
    if (!blob || !type || (scale === 1 && blob.size >= file.size)) {
      return { file, profile: captureFallbackProfile, changed: false };
    }

    const prepared = new File([blob], outputName(file.name, type), { type, lastModified: file.lastModified });
    return { file: prepared, profile: `capture-2048-${type === "image/webp" ? "webp" : "jpeg"}85-v1`, changed: true };
  } catch {
    return { file, profile: captureFallbackProfile, changed: false };
  } finally {
    bitmap?.close();
  }
}
