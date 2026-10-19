import type { Page } from "@playwright/test";

type FakeGetUserMediaOptions =
  | { kind: "tracks" }
  | { kind: "permission-denied" }
  | { kind: "not-readable" }
  | { kind: "canvas"; width: number; height: number; highEntropy?: boolean; captureConstraints?: boolean };

export async function installFakeGetUserMedia(page: Page, options: FakeGetUserMediaOptions): Promise<void> {
  await page.addInitScript((config) => {
    let mediaDevices = navigator.mediaDevices;
    if (!mediaDevices) {
      mediaDevices = {} as MediaDevices;
      Object.defineProperty(navigator, "mediaDevices", { configurable: true, value: mediaDevices });
    }

    let fake: (constraints?: MediaStreamConstraints) => Promise<MediaStream | { getTracks: () => Array<{ stop(): void }> }>;
    if (config.kind === "permission-denied") {
      fake = async () => { throw new DOMException("Permission denied", "NotAllowedError"); };
    } else if (config.kind === "not-readable") {
      fake = async () => { throw new DOMException("Camera is already in use", "NotReadableError"); };
    } else if (config.kind === "tracks") {
      fake = async () => ({ getTracks: () => [{ stop() {} }] });
    } else {
      const canvas = document.createElement("canvas");
      canvas.width = config.width;
      canvas.height = config.height;
      const context = canvas.getContext("2d");
      let frame = 0;
      const pixels = config.highEntropy && context ? context.createImageData(canvas.width, canvas.height) : undefined;
      setInterval(() => {
        if (!context) return;
        if (pixels) {
          const words = new Uint32Array(pixels.data.buffer);
          let state = (frame++ + 1) * 2654435761;
          for (let index = 0; index < words.length; index += 1) {
            state ^= state << 13; state ^= state >>> 17; state ^= state << 5;
            words[index] = (state & 0x00ffffff) | 0xff000000;
          }
          context.putImageData(pixels, 0, 0);
        } else {
          context.fillStyle = frame++ % 2 ? "#2367aa" : "#aacf34";
          context.fillRect(0, 0, canvas.width, canvas.height);
        }
      }, config.highEntropy ? 33 : 50);
      fake = async (constraints) => {
        if (config.captureConstraints) {
          (window as typeof window & { captureConstraints?: MediaStreamConstraints }).captureConstraints = constraints;
        }
        return canvas.captureStream(30);
      };
    }

    const proto = (window as typeof window & { MediaDevices?: { prototype: object } }).MediaDevices?.prototype ?? Object.getPrototypeOf(mediaDevices);
    Object.defineProperty(proto, "getUserMedia", { configurable: true, writable: true, value: fake });
  }, options);
}
