import "fake-indexeddb/auto";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act } from "react";
import { button, click, createElement, key, mount, pointer, settle, silenceActWarnings, unmountAll, waitFor } from "./support/dom";
import { VoiceNoteControl, type VoiceTarget } from "../../app/capture/[linkToken]/voice-note-control";

const clock = vi.hoisted(() => ({ value: 0 }));
const uploads = vi.hoisted(() => ({ upload: vi.fn() }));
vi.mock("@/pwa/audio-uploads", () => ({ uploadAudioDraft: uploads.upload }));
vi.mock("@/pwa/audio-recorder", async (importOriginal) => {
  const real = await importOriginal<typeof import("@/pwa/audio-recorder")>();
  return { ...real, startAudioRecorder: (input: Parameters<typeof real.startAudioRecorder>[0]) => real.startAudioRecorder({ ...input, now: () => clock.value, digest: async () => "a".repeat(64) }) };
});

class FakeMediaRecorder {
  static isTypeSupported = vi.fn(() => true);
  state: "inactive" | "recording" = "inactive";
  mimeType: string;
  ondataavailable?: (event: { data: Blob }) => void;
  onstop?: () => void;
  onerror?: () => void;
  constructor(_stream: MediaStream, options?: MediaRecorderOptions) { this.mimeType = options?.mimeType ?? "audio/webm"; }
  start() { this.state = "recording"; }
  stop() { this.ondataavailable?.({ data: new Blob(["voice"], { type: this.mimeType }) }); this.state = "inactive"; this.onstop?.(); }
}

const deferred = <T,>() => { let resolve!: (value: T) => void; let reject!: (error: unknown) => void; const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; }); return { promise, resolve, reject }; };
function microphone() {
  const track = new EventTarget() as MediaStreamTrack;
  const stop = vi.fn();
  Object.defineProperties(track, { readyState: { value: "live" }, stop: { value: stop } });
  return { stop, stream: { getTracks: () => [track], getAudioTracks: () => [track] } as unknown as MediaStream, track };
}

const photoTarget: VoiceTarget = { kind: "PHOTO", photoDraftId: "photo-draft-1", mediaId: "photo-1" };
const baseProps = { responsibilityId: "responsibility-1", requirementKey: "env:living", environmentLabel: "Sala", disclosureVersion: "pt-BR-1", consentVersion: "voice-v1", enabled: true, online: true, target: photoTarget };
const getUserMedia = vi.fn();

function render(props: Partial<Parameters<typeof VoiceNoteControl>[0]> = {}) {
  const view = mount(createElement(VoiceNoteControl, { ...baseProps, ...props }));
  const control = () => view.container.querySelector<HTMLButtonElement>("button")!;
  const status = () => view.container.querySelector("[role=status]")?.textContent ?? "";
  return { ...view, control, status };
}
const grant = async () => { const mic = microphone(); getUserMedia.mockResolvedValueOnce(mic.stream); return mic; };

describe("VoiceNoteControl", () => {
  beforeEach(async () => {
    silenceActWarnings();
    clock.value = 0;
    getUserMedia.mockReset();
    uploads.upload.mockReset().mockImplementation(async (draft) => ({ ...draft, state: "RECEIVED", audio: undefined }));
    vi.stubGlobal("MediaRecorder", FakeMediaRecorder);
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia }, onLine: true });
    await new Promise<void>((resolve, reject) => { const request = indexedDB.deleteDatabase("inspection-audio-notes-v1"); request.onsuccess = () => resolve(); request.onerror = () => reject(request.error); });
  });
  afterEach(() => { unmountAll(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

  it("IT-030 shows preparing first and recording only once the microphone is really recording", async () => {
    const pending = deferred<MediaStream>();
    getUserMedia.mockReturnValueOnce(pending.promise);
    const { control, status } = render();
    expect(control().getAttribute("aria-label")).toBe("Segure para falar ou pressione Enter ou Espaço");
    pointer(control(), "pointerdown");
    expect(status()).toBe("Preparando o microfone…");
    expect(control().textContent).toContain("Preparando microfone…");
    const mic = microphone();
    await act(async () => { pending.resolve(mic.stream); await pending.promise; });
    await waitFor(() => control().getAttribute("aria-label") === "Encerrar anotação de voz", "recording state");
    expect(status()).toBe("Gravando. Solte o botão para encerrar.");
    expect(getUserMedia).toHaveBeenCalledWith({ audio: true, video: false });
  });

  it("IT-041 / IT-025 / UT-008 releasing before the microphone opens never starts a late recording", async () => {
    const pending = deferred<MediaStream>();
    getUserMedia.mockReturnValueOnce(pending.promise);
    const { control, status } = render();
    pointer(control(), "pointerdown");
    pointer(window, "pointerup");
    expect(status()).toBe("A solicitação do microfone foi cancelada. Pressione novamente para falar.");
    const late = microphone();
    await act(async () => { pending.resolve(late.stream); await pending.promise; });
    await settle(4);
    expect(late.stop).toHaveBeenCalled();
    expect(control().getAttribute("aria-label")).toBe("Segure para falar ou pressione Enter ou Espaço");
    expect(uploads.upload).not.toHaveBeenCalled();
  });

  it("IT-031 / IT-040 ends a held note once however many release events arrive, and sends it once", async () => {
    const mic = await grant();
    const { control, status } = render();
    pointer(control(), "pointerdown", 7);
    await waitFor(() => control().getAttribute("aria-label") === "Encerrar anotação de voz", "recording");
    clock.value = 900;
    pointer(window, "pointerup", 99);
    expect(control().getAttribute("aria-label")).toBe("Encerrar anotação de voz");
    pointer(window, "pointerup", 7);
    pointer(window, "pointerup", 7);
    pointer(window, "pointercancel", 7);
    await waitFor(() => status() === "Anotação recebida e aguardando processamento.", "received message");
    expect(uploads.upload).toHaveBeenCalledTimes(1);
    expect(uploads.upload.mock.calls[0][0]).toMatchObject({ requirementKey: "env:living", durationMs: 900, target: { kind: "PHOTO", photoDraftId: "photo-draft-1", mediaId: "photo-1" } });
    expect(mic.stop).toHaveBeenCalled();
  });

  it("IT-057 / IT-058 / IT-060 / IT-066 starts and stops with Enter or Space, ignoring key repeat and other keys", async () => {
    await grant();
    const { control, status } = render();
    expect(key(control(), "keydown", "a").defaultPrevented).toBe(false);
    expect(getUserMedia).not.toHaveBeenCalled();
    const first = key(control(), "keydown", "Enter");
    expect(first.defaultPrevented).toBe(true);
    key(control(), "keydown", "Enter", true);
    key(control(), "keydown", "Enter", true);
    key(control(), "keyup", "Enter");
    await waitFor(() => control().getAttribute("aria-label") === "Encerrar anotação de voz", "recording by keyboard");
    expect(status()).toBe("Gravando. Pressione Enter ou Espaço novamente para encerrar.");
    expect(getUserMedia).toHaveBeenCalledTimes(1);
    clock.value = 800;
    key(control(), "keydown", " ");
    key(control(), "keydown", " ", true);
    key(control(), "keyup", " ");
    await waitFor(() => status() === "Anotação recebida e aguardando processamento.", "received");
    expect(uploads.upload).toHaveBeenCalledTimes(1);
  });

  it("IT-064 / IT-038 never lets overlapping pointer and keyboard activations start a second recording", async () => {
    const pending = deferred<MediaStream>();
    getUserMedia.mockReturnValueOnce(pending.promise);
    const { control } = render();
    key(control(), "keydown", "Enter");
    pointer(control(), "pointerdown");
    key(control(), "keydown", " ");
    expect(getUserMedia).toHaveBeenCalledTimes(1);
  });

  it("IT-067 a second activation before the microphone opens cancels the pending start", async () => {
    const pending = deferred<MediaStream>();
    getUserMedia.mockReturnValueOnce(pending.promise);
    const { control, status } = render();
    key(control(), "keydown", "Enter");
    expect(status()).toBe("Preparando o microfone…");
    key(control(), "keydown", "Enter");
    expect(status()).toMatch(/cancelada/);
    const late = microphone();
    await act(async () => { pending.resolve(late.stream); await pending.promise; });
    await settle(4);
    expect(late.stop).toHaveBeenCalled();
    expect(uploads.upload).not.toHaveBeenCalled();
    expect(control().getAttribute("aria-label")).toBe("Segure para falar ou pressione Enter ou Espaço");
  });

  it("a click anywhere does not end a note that was started with the keyboard, but pressing the control does", async () => {
    await grant();
    const { control, status } = render();
    key(control(), "keydown", " ");
    await waitFor(() => control().getAttribute("aria-label") === "Encerrar anotação de voz", "recording");
    pointer(window, "pointerup", 1);
    expect(control().getAttribute("aria-label")).toBe("Encerrar anotação de voz");
    clock.value = 900;
    pointer(control(), "pointerdown", 3);
    await waitFor(() => status() === "Anotação recebida e aguardando processamento.", "received");
    expect(uploads.upload).toHaveBeenCalledTimes(1);
  });

  it("IT-057 assistive technology activation (a click without pointer or key) toggles the note", async () => {
    await grant();
    const { control, status } = render();
    click(control(), 1);
    expect(getUserMedia).not.toHaveBeenCalled();
    click(control(), 0);
    await waitFor(() => control().getAttribute("aria-label") === "Encerrar anotação de voz", "recording by assistive activation");
    clock.value = 700;
    click(control(), 0);
    await waitFor(() => status() === "Anotação recebida e aguardando processamento.", "received");
    expect(uploads.upload).toHaveBeenCalledTimes(1);
  });

  it.each([
    ["IT-065 loss of focus", () => window.dispatchEvent(new Event("blur")), /perdeu o foco/],
    ["IT-039 page hidden", () => { Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "hidden" }); document.dispatchEvent(new Event("visibilitychange")); }, /primeiro plano/],
    ["IT-039 page hide", () => window.dispatchEvent(new Event("pagehide")), /primeiro plano/],
    ["IT-068 video end", () => window.dispatchEvent(new Event("inspection-video-end")), /junto com o vídeo/]
  ])("%s closes the active note once and shows its saved state", async (_name, interrupt, message) => {
    await grant();
    const { control, status } = render();
    pointer(control(), "pointerdown");
    await waitFor(() => control().getAttribute("aria-label") === "Encerrar anotação de voz", "recording");
    clock.value = 900;
    interrupt(); interrupt();
    await waitFor(() => /Anotação recebida/.test(status()), "received");
    expect(status()).toMatch(message);
    expect(uploads.upload).toHaveBeenCalledTimes(1);
    Object.defineProperty(document, "visibilityState", { configurable: true, get: () => "visible" });
  });

  it("IT-031 / IT-068 keeps the exact video interval from the capture clock when the clip ends the note", async () => {
    await grant();
    const { control } = render({ target: { kind: "VIDEO", attemptId: "attempt-1", clipStartedAt: 100 } });
    clock.value = 1100;
    pointer(control(), "pointerdown");
    await waitFor(() => control().getAttribute("aria-label") === "Encerrar anotação de voz", "recording");
    clock.value = 1900;
    window.dispatchEvent(new Event("inspection-video-end"));
    await waitFor(() => uploads.upload.mock.calls.length === 1, "upload");
    expect(uploads.upload.mock.calls[0][0].target).toEqual({ kind: "VIDEO", attemptId: "attempt-1", startMs: 1000, endMs: 1800, timeBasis: "CAPTURE_CLOCK" });
  });

  it("IT-033 / IT-035 discards a note shorter than 500 ms with guidance and sends nothing", async () => {
    await grant();
    const { control, status } = render();
    pointer(control(), "pointerdown");
    await waitFor(() => control().getAttribute("aria-label") === "Encerrar anotação de voz", "recording");
    clock.value = 300;
    pointer(window, "pointerup");
    await waitFor(() => /meio segundo/.test(status()), "short note guidance");
    expect(uploads.upload).not.toHaveBeenCalled();
  });

  it("IT-023 / IT-037 / IT-063 explains a denied microphone in Portuguese and stays ready for another try", async () => {
    getUserMedia.mockRejectedValueOnce(new DOMException("Permission denied", "NotAllowedError"));
    const { control, status } = render();
    key(control(), "keydown", "Enter");
    await waitFor(() => /acesso ao microfone foi negado/.test(status()), "denial message");
    expect(status()).not.toMatch(/Permission denied/);
    expect(status()).toMatch(/Fotos e vídeos continuam disponíveis/);
    expect(control().getAttribute("aria-label")).toBe("Segure para falar ou pressione Enter ou Espaço");
    expect(control().getAttribute("aria-disabled")).toBeNull();
  });

  it("IT-061 explains how to enable voice and never opens the microphone without consent", async () => {
    const { control, status } = render({ enabled: false });
    expect(control().getAttribute("aria-disabled")).toBe("true");
    expect(status()).toMatch(/Ative as anotações de voz/);
    pointer(control(), "pointerdown");
    key(control(), "keydown", "Enter");
    expect(getUserMedia).not.toHaveBeenCalled();
  });

  it("IT-048 prompts for a photo when none is selected", async () => {
    const { control, status } = render({ target: undefined });
    expect(control().getAttribute("aria-disabled")).toBe("true");
    expect(status()).toMatch(/Selecione ou tire uma foto/);
    pointer(control(), "pointerdown");
    expect(getUserMedia).not.toHaveBeenCalled();
  });

  it("IT-049 / IT-076 blocks new notes with the visible allowance reason", async () => {
    const { control, status } = render({ blockedReason: "O limite de anotações de voz foi atingido. Fotos e vídeos continuam disponíveis." });
    expect(control().getAttribute("aria-disabled")).toBe("true");
    expect(status()).toMatch(/limite de anotações de voz/);
    key(control(), "keydown", "Enter");
    expect(getUserMedia).not.toHaveBeenCalled();
  });

  it("IT-059 prevents the touch context menu and gives the control a stable accessible name", () => {
    const { control } = render();
    const menu = new MouseEvent("contextmenu", { bubbles: true, cancelable: true });
    act(() => { control().dispatchEvent(menu); });
    expect(menu.defaultPrevented).toBe(true);
    expect(control().getAttribute("draggable")).toBe("false");
    expect(control().getAttribute("aria-describedby")).toBeTruthy();
    expect(control().getAttribute("aria-pressed")).toBeNull();
  });

  it("IT-070 saves a note recorded offline and says its upload waits for the connection", async () => {
    await grant();
    const { control, status } = render({ online: false });
    pointer(control(), "pointerdown");
    await waitFor(() => control().getAttribute("aria-label") === "Encerrar anotação de voz", "recording");
    clock.value = 900;
    pointer(window, "pointerup");
    await waitFor(() => /pendente até a conexão voltar/.test(status()), "offline message");
    expect(uploads.upload).not.toHaveBeenCalled();
  });

  it("IT-052 says a photo note waits for its photo, and another tab's upload is not reported as a photo wait", async () => {
    uploads.upload.mockImplementationOnce(async (saved) => ({ ...saved, target: { kind: "PHOTO", photoDraftId: "photo-draft-1" } }));
    await grant();
    const waiting = render({ target: { kind: "PHOTO", photoDraftId: "photo-draft-1" } });
    pointer(waiting.control(), "pointerdown");
    await waitFor(() => waiting.control().getAttribute("aria-label") === "Encerrar anotação de voz", "recording");
    clock.value = 900;
    pointer(window, "pointerup");
    await waitFor(() => /será enviada assim que a foto for enviada/.test(waiting.status()), "waiting for the photo");
    unmountAll();
    uploads.upload.mockImplementationOnce(async (saved) => saved);
    await grant();
    const shared = render();
    pointer(shared.control(), "pointerdown");
    await waitFor(() => shared.control().getAttribute("aria-label") === "Encerrar anotação de voz", "recording again");
    clock.value = 1_800;
    pointer(window, "pointerup");
    await waitFor(() => /o envio continua em andamento/.test(shared.status()), "upload in progress elsewhere");
  });

  it("IT-022 / IT-076 shows the allowance explanation when admission is refused", async () => {
    uploads.upload.mockRejectedValueOnce({ code: "LIMIT_REACHED", message: "limit reached" });
    await grant();
    const { control, status } = render();
    pointer(control(), "pointerdown");
    await waitFor(() => control().getAttribute("aria-label") === "Encerrar anotação de voz", "recording");
    clock.value = 900;
    pointer(window, "pointerup");
    await waitFor(() => /limite de uso de voz/.test(status()), "limit message");
    expect(status()).not.toMatch(/limit reached/);
  });

  it("IT-054 tells the page when a note is active so the photo stays fixed, and when it ends", async () => {
    await grant();
    const events: boolean[] = [];
    const listener = (event: Event) => events.push(Boolean((event as CustomEvent<{ recording: boolean }>).detail.recording));
    window.addEventListener("inspection-audio-state", listener);
    const { control, status } = render();
    pointer(control(), "pointerdown");
    await waitFor(() => control().getAttribute("aria-label") === "Encerrar anotação de voz", "recording");
    clock.value = 900;
    pointer(window, "pointerup");
    await waitFor(() => status() === "Anotação recebida e aguardando processamento.", "received");
    window.removeEventListener("inspection-audio-state", listener);
    expect(events[0]).toBe(true);
    expect(events.at(-1)).toBe(false);
  });

  it("releases the microphone when the control disappears while recording", async () => {
    const mic = await grant();
    const view = render();
    pointer(view.control(), "pointerdown");
    await waitFor(() => view.control().getAttribute("aria-label") === "Encerrar anotação de voz", "recording");
    clock.value = 900;
    unmountAll();
    await waitFor(() => mic.stop.mock.calls.length > 0, "microphone release");
    await waitFor(() => uploads.upload.mock.calls.length === 1, "background upload of the finished note");
    expect(button(document.body, /Segure/)).toBeUndefined();
  });
});
