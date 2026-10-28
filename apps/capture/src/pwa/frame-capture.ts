/** Captures the full current frame of a live camera preview as a JPEG File, without cropping. */
export async function captureFrame(video: HTMLVideoElement, options?: { now?: () => Date; track?: MediaStreamTrack }): Promise<File> {
  if (options?.track && options.track.readyState !== "live") throw new Error("camera-frame-unavailable");
  const width = video.videoWidth;
  const height = video.videoHeight;
  if (!width || !height) throw new Error("camera-frame-unavailable");

  let blob: Blob | null = null;
  if (typeof globalThis.OffscreenCanvas !== "undefined") {
    try {
      const canvas = new OffscreenCanvas(width, height);
      const context = canvas.getContext("2d");
      if (context) {
        context.drawImage(video, 0, 0, width, height);
        blob = await canvas.convertToBlob({ type: "image/jpeg", quality: 0.92 });
      }
    } catch {
      blob = null;
    }
  }

  if (!blob) {
    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    try {
      const context = canvas.getContext("2d");
      if (!context) throw new Error("camera-frame-unavailable");
      context.drawImage(video, 0, 0, width, height);
      blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/jpeg", 0.92));
    } catch {
      blob = null;
    } finally {
      canvas.width = 0;
      canvas.height = 0;
    }
  }

  if (!blob) throw new Error("camera-frame-unavailable");
  const date = (options?.now ?? (() => new Date()))();
  const pad = (value: number) => String(value).padStart(2, "0");
  const name = `camera-${date.getFullYear()}${pad(date.getMonth() + 1)}${pad(date.getDate())}-${pad(date.getHours())}${pad(date.getMinutes())}${pad(date.getSeconds())}.jpg`;
  return new File([blob], name, { type: blob.type || "image/jpeg" });
}
