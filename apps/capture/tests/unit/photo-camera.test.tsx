import { act, createElement } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PhotoCamera } from "../../app/capture/[linkToken]/photo-camera";

const cameraMocks = vi.hoisted(() => ({ open: vi.fn(), attach: vi.fn(), play: vi.fn(), capture: vi.fn(), release: vi.fn() }));
vi.mock("@/pwa/camera-session", () => ({
  openPhotoCameraCancellable: (cancelled: () => boolean) => cameraMocks.open().then((stream: MediaStream) => cancelled() ? (cameraMocks.release(stream), undefined) : stream),
  attachPreview: cameraMocks.attach,
  playPreview: cameraMocks.play,
  releaseCamera: cameraMocks.release,
  classifyCameraError: (error: unknown) => typeof error === "object" && error !== null && "name" in error && error.name === "NotAllowedError" ? "denied" : "unknown",
  isInAppBrowser: () => false
}));
vi.mock("@/pwa/frame-capture", () => ({ captureFrame: cameraMocks.capture }));
vi.mock("@/pwa/wake-lock", () => ({ acquireScreenWakeLock: async () => async () => {} }));

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
const roots: Array<{ root: ReturnType<typeof createRoot>; container: HTMLDivElement }> = [];
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
};
const streamFixture = () => {
  const track = { readyState: "live", stop: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn() } as unknown as MediaStreamTrack;
  return { track, stream: { getTracks: () => [track], getVideoTracks: () => [track] } as unknown as MediaStream };
};
const baseProps = () => ({
  environmentLabel: "Cozinha", photoCount: 0, minimumMedia: 1, maximumMedia: 20,
  items: [{ key: "env:cozinha", label: "Cozinha", current: true, done: false }], onSelectItem: vi.fn(),
  galleryAllowed: true, onGallery: vi.fn(), onDeviceCamera: vi.fn(), impossibilityAllowed: true, onImpossible: vi.fn(), onClose: vi.fn(),
  onCapture: vi.fn(async () => {}), onCaptureError: vi.fn(), onUsePreview: vi.fn(async () => true), onRetake: vi.fn(), busy: false, locating: false, onDismissToast: vi.fn()
});
function render(props = baseProps()) {
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container); roots.push({ root, container });
  act(() => root.render(createElement(PhotoCamera, props)));
  return { root, container };
}
function setReady(container: HTMLElement) {
  const video = container.ownerDocument.querySelector<HTMLVideoElement>('video[aria-label="Imagem ao vivo da câmera"]')!;
  Object.defineProperty(video, "readyState", { configurable: true, value: 2 });
  Object.defineProperty(video, "videoWidth", { configurable: true, value: 960 });
  Object.defineProperty(video, "videoHeight", { configurable: true, value: 1280 });
  act(() => video.dispatchEvent(new Event("loadeddata")));
}
beforeEach(() => {
  cameraMocks.open.mockReset(); cameraMocks.attach.mockReset(); cameraMocks.play.mockReset(); cameraMocks.capture.mockReset(); cameraMocks.release.mockReset();
  cameraMocks.attach.mockImplementation(async (video: HTMLVideoElement, stream: MediaStream) => { video.srcObject = stream; return "playing"; });
  cameraMocks.play.mockResolvedValue("playing");
});
afterEach(() => {
  for (const { root, container } of roots.splice(0)) { act(() => root.unmount()); container.remove(); }
  document.body.querySelectorAll(".camera-stage").forEach((element) => element.remove());
});

describe("PhotoCamera", () => {
  it("releases permission granted after the camera is closed", async () => {
    const pending = deferred<MediaStream>(); const fixture = streamFixture();
    cameraMocks.open.mockReturnValue(pending.promise);
    const { root, container } = render();
    await act(async () => { await Promise.resolve(); });
    act(() => document.querySelector<HTMLElement>(".camera-stage")?.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    act(() => root.unmount());
    await act(async () => { pending.resolve(fixture.stream); await pending.promise; await Promise.resolve(); });
    expect(cameraMocks.release).toHaveBeenCalledWith(fixture.stream);
    container.remove();
  });

  it("opens only one capture for two quick shutter presses", async () => {
    const fixture = streamFixture(); cameraMocks.open.mockResolvedValue(fixture.stream); cameraMocks.capture.mockResolvedValue(new File(["photo"], "photo.jpg", { type: "image/jpeg" }));
    const props = baseProps(); const { container } = render(props);
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
    setReady(container);
    const button = document.querySelector<HTMLButtonElement>('.camera-stage button[aria-label="Fotografar"]')!;
    await act(async () => { button.click(); button.click(); await Promise.resolve(); });
    expect(cameraMocks.capture).toHaveBeenCalledOnce();
    expect(props.onCapture).toHaveBeenCalledOnce();
    expect(props.onCapture).toHaveBeenCalledWith(expect.any(File), "env:cozinha");
  });

  it("invalidates the hidden opening and performs one opening when visible again", async () => {
    const first = deferred<MediaStream>(); const second = deferred<MediaStream>(); const fixture = streamFixture();
    cameraMocks.open.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    render();
    await act(async () => { await Promise.resolve(); });
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "hidden" });
    await act(async () => { document.dispatchEvent(new Event("visibilitychange")); });
    Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
    await act(async () => { document.dispatchEvent(new Event("visibilitychange")); await Promise.resolve(); });
    first.resolve(fixture.stream);
    await act(async () => { await first.promise; await Promise.resolve(); });
    expect(cameraMocks.open).toHaveBeenCalledTimes(2);
    expect(cameraMocks.release).toHaveBeenCalledWith(fixture.stream);
  });

  it("reports frame capture failures to the page", async () => {
    const fixture = streamFixture(); cameraMocks.open.mockResolvedValue(fixture.stream); cameraMocks.capture.mockRejectedValue(new Error("camera-frame-unavailable"));
    const props = baseProps(); const { container } = render(props);
    await act(async () => { await Promise.resolve(); await Promise.resolve(); }); setReady(container);
    await act(async () => { document.querySelector<HTMLButtonElement>('.camera-stage button[aria-label="Fotografar"]')!.click(); await Promise.resolve(); await Promise.resolve(); });
    expect(props.onCaptureError).toHaveBeenCalledWith(expect.objectContaining({ message: "camera-frame-unavailable" }));
  });

  it("reports a camera stream whose track already ended", async () => {
    const fixture = streamFixture();
    Object.defineProperty(fixture.track, "readyState", { configurable: true, value: "ended" });
    cameraMocks.open.mockResolvedValue(fixture.stream);
    render();
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
    expect(cameraMocks.release).toHaveBeenCalledWith(fixture.stream);
    expect(document.querySelector('[aria-label="Não foi possível abrir a câmera"]')).not.toBeNull();
  });

  it("hides camera failures when a fallback photo is under review and shows notices", async () => {
    cameraMocks.open.mockRejectedValue(new DOMException("blocked", "NotAllowedError"));
    const props = baseProps();
    const { root } = render(props);
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
    expect(document.querySelector('[aria-label="A câmera está bloqueada"]')).not.toBeNull();
    await act(async () => { root.render(createElement(PhotoCamera, { ...props, preview: { url: "blob:preview", warnings: [] }, notice: "A localização continua pendente." })); });
    expect(document.querySelector('[aria-label="A câmera está bloqueada"]')).toBeNull();
    expect(document.querySelector(".camera-stage__notice")?.textContent).toContain("A localização continua pendente.");
    expect(document.querySelector('img[alt="Prévia da foto capturada"]')).not.toBeNull();
  });
});
