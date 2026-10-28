const constraints: MediaStreamConstraints = {
  audio: false,
  video: { facingMode: { ideal: "environment" }, width: { ideal: 1920 }, height: { ideal: 1080 }, frameRate: { ideal: 30 } }
};

/** Constraints for still photos: prefer a 4:3 high-resolution rear stream. */
export const photoConstraints: MediaStreamConstraints = {
  audio: false,
  video: { facingMode: { ideal: "environment" }, width: { ideal: 4032 }, height: { ideal: 3024 }, aspectRatio: { ideal: 4 / 3 } }
};

/** Error raised when the browser cannot provide camera access. */
export class CameraUnsupportedError extends Error {
  constructor() {
    super("Este navegador não oferece acesso à câmera. Abra o convite no Safari ou Chrome atualizado do aparelho.");
    this.name = "CameraUnsupportedError";
  }
}

/** Error raised when camera access is attempted outside a secure context. */
export class CameraInsecureContextError extends Error {
  constructor() {
    super("A câmera só funciona em uma conexão segura.");
    this.name = "CameraInsecureContextError";
  }
}

/** Camera failure categories shown to the person. */
export type CameraFailure = "denied" | "busy" | "unavailable" | "insecure" | "unsupported" | "unknown";

/** Maps getUserMedia errors to a CameraFailure. */
export function classifyCameraError(error: unknown): CameraFailure {
  const name = typeof error === "object" && error !== null && "name" in error ? String((error as { name?: unknown }).name ?? "") : "";
  if (error instanceof CameraInsecureContextError || name === "CameraInsecureContextError") return "insecure";
  if (error instanceof CameraUnsupportedError || name === "CameraUnsupportedError") return "unsupported";
  if (["NotAllowedError", "PermissionDeniedError"].includes(name)) return "denied";
  if (["NotReadableError", "TrackStartError", "AbortError"].includes(name)) return "busy";
  if (["NotFoundError", "DevicesNotFoundError", "OverconstrainedError"].includes(name)) return "unavailable";
  if (name === "SecurityError") return "insecure";
  return "unknown";
}

/** True when the user agent looks like an in-app browser that commonly blocks the camera. */
export function isInAppBrowser(userAgent: string): boolean {
  return /Instagram|FBAN|FBAV|FB_IAB|Line\/|WhatsApp|TikTok|musical_ly|Snapchat|LinkedInApp|GSA\/|; wv\)/i.test(userAgent);
}

export function canOpenCamera(): boolean {
  return typeof navigator !== "undefined" && typeof navigator.mediaDevices?.getUserMedia === "function";
}

export async function openCamera(): Promise<MediaStream> {
  if (globalThis.isSecureContext === false) throw new CameraInsecureContextError();
  if (!canOpenCamera()) throw new CameraUnsupportedError();
  return navigator.mediaDevices.getUserMedia(constraints);
}

/** Opens the rear camera for still photos, retrying with the video constraints on OverconstrainedError. */
export async function openPhotoCamera(): Promise<MediaStream> {
  if (globalThis.isSecureContext === false) throw new CameraInsecureContextError();
  if (!canOpenCamera()) throw new CameraUnsupportedError();
  try {
    return await navigator.mediaDevices.getUserMedia(photoConstraints);
  } catch (error) {
    if (typeof error === "object" && error !== null && "name" in error && (error as { name?: unknown }).name === "OverconstrainedError") {
      return navigator.mediaDevices.getUserMedia(constraints);
    }
    throw error;
  }
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

/** Opens the still-photo camera; a permission granted after cancellation has its tracks stopped. */
export async function openPhotoCameraCancellable(isCancelled: () => boolean): Promise<MediaStream | undefined> {
  const stream = await openPhotoCamera();
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
