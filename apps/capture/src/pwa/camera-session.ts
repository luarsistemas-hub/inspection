const constraints: MediaStreamConstraints = {
  audio: false,
  video: { facingMode: { ideal: "environment" }, width: { ideal: 1920 }, height: { ideal: 1080 }, frameRate: { ideal: 30 } }
};

export function canOpenCamera(): boolean {
  return typeof navigator !== "undefined" && typeof navigator.mediaDevices?.getUserMedia === "function";
}

export async function openCamera(): Promise<MediaStream> {
  if (!canOpenCamera()) throw new Error("Este navegador não oferece acesso à câmera. Abra o convite no Safari ou Chrome atualizado do aparelho.");
  return navigator.mediaDevices.getUserMedia(constraints);
}

export function releaseCamera(stream?: MediaStream): void {
  stream?.getTracks().forEach((track) => track.stop());
}

/** Opens the camera; a permission granted after cancellation has its tracks stopped and yields `undefined`. */
export async function openCameraCancellable(isCancelled: () => boolean): Promise<MediaStream | undefined> {
  const stream = await openCamera();
  if (isCancelled()) { releaseCamera(stream); return undefined; }
  return stream;
}

/** Plays the preview element; `blocked` means autoplay was refused and the user must activate it. */
export async function playPreview(video: Pick<HTMLVideoElement, "play">): Promise<"playing" | "blocked"> {
  try { await video.play(); return "playing"; } catch { return "blocked"; }
}

export function attachPreview(video: Pick<HTMLVideoElement, "play" | "srcObject">, stream: MediaStream): Promise<"playing" | "blocked"> {
  video.srcObject = stream;
  return playPreview(video);
}
