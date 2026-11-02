import "fake-indexeddb/auto";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { button, click, createElement, mount, pointer, settle, silenceActWarnings, unmountAll, waitFor } from "./support/dom";
import { PhotoVoiceNotes, type VoicePhoto } from "../../app/capture/[linkToken]/voice-photo-notes";
import { VoiceSubmissionSummary } from "../../app/capture/[linkToken]/voice-submission-summary";
import { VoiceOptIn } from "../../app/capture/[linkToken]/voice-opt-in";
import type { AudioDraft } from "@/pwa/audio-drafts";
import type { NoteItem } from "@/pwa/audio-notes-state";

const clock = vi.hoisted(() => ({ value: 0 }));
const uploads = vi.hoisted(() => ({ upload: vi.fn() }));
vi.mock("@/pwa/audio-uploads", () => ({ uploadAudioDraft: uploads.upload }));
vi.mock("@/graphql/client", () => ({ graphql: vi.fn() }));
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
  constructor(_stream: MediaStream, options?: MediaRecorderOptions) { this.mimeType = options?.mimeType ?? "audio/webm"; }
  start() { this.state = "recording"; }
  stop() { this.ondataavailable?.({ data: new Blob(["voice"], { type: this.mimeType }) }); this.state = "inactive"; this.onstop?.(); }
}
const microphone = () => {
  const track = new EventTarget() as MediaStreamTrack;
  Object.defineProperties(track, { readyState: { value: "live" }, stop: { value: vi.fn() } });
  return { getTracks: () => [track], getAudioTracks: () => [track] } as unknown as MediaStream;
};
const getUserMedia = vi.fn();

const photo = (id: string, mediaId: string | undefined, capturedAt: string): VoicePhoto => ({ draftId: id, mediaId, blob: new Blob(["img"], { type: "image/png" }), capturedAt });
const note = (id: string, target: AudioDraft["target"], overrides: Partial<AudioDraft> = {}): NoteItem => ({ draft: { id, responsibilityId: "responsibility-1", requirementKey: "env:living", disclosureVersion: "pt-BR-1", consentVersion: "voice-v1", target, durationMs: 800, contentType: "audio/webm", sizeBytes: 5, sha256: "a".repeat(64), state: "RECEIVED", noteId: `server-${id}`, createdAt: "2026-11-01T00:00:00Z", ...overrides } });
const reload = vi.fn(async () => undefined);

type Props = Parameters<typeof PhotoVoiceNotes>[0];
function render(overrides: Partial<Props> = {}) {
  const props: Props = { responsibilityId: "responsibility-1", requirementKey: "env:living", environmentLabel: "Sala", disclosureVersion: "pt-BR-1", consentVersion: "voice-v1", consented: true, online: true, photos: [], voice: { items: [], loaded: true, reload }, onEnableVoice: vi.fn(async () => undefined), ...overrides };
  const view = mount(createElement(PhotoVoiceNotes, props));
  const text = () => view.container.textContent ?? "";
  const control = () => button(view.container, /Segure para falar|Encerrar anotação de voz|Preparando/);
  return { ...view, props, text, control, radios: () => [...view.container.querySelectorAll<HTMLInputElement>("input[type=radio]")] };
}

describe("photo voice notes", () => {
  beforeEach(() => {
    silenceActWarnings();
    clock.value = 0; getUserMedia.mockReset(); uploads.upload.mockReset().mockImplementation(async (draft) => ({ ...draft, state: "RECEIVED", audio: undefined })); reload.mockClear();
    vi.stubGlobal("MediaRecorder", FakeMediaRecorder);
    vi.stubGlobal("navigator", { mediaDevices: { getUserMedia }, onLine: true });
    URL.createObjectURL = vi.fn(() => "blob:photo"); URL.revokeObjectURL = vi.fn();
  });
  afterEach(() => { unmountAll(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

  it("IT-016 / IT-019 / IT-021 / IT-027 explains voice use before any recording and never opens the microphone for it", () => {
    const view = render({ consented: false, photos: [photo("p1", "photo-1", "2026-11-01T10:00:00Z")] });
    expect(view.text()).toContain("Anotações de voz (opcional)");
    expect(view.control()).toBeUndefined();
    expect(getUserMedia).not.toHaveBeenCalled();
    click(button(view.container, "Ativar anotações de voz")!);
    expect(view.text()).toContain("O microfone só será solicitado quando você iniciar uma anotação, a cada gravação.");
    expect(view.text()).toContain("removido da nossa cópia após a confirmação de recebimento durável");
    expect(view.text()).toContain("transcrição");
    expect(view.text()).toContain("A autorização das fotos e dos vídeos não vale para a voz.");
    expect(button(view.container, "Autorizar anotações de voz")!.disabled).toBe(true);
    expect(getUserMedia).not.toHaveBeenCalled();
  });

  it("IT-026 sends one authorization however often it is pressed, then requires a new press to record", async () => {
    const onEnable = vi.fn(() => new Promise<void>((resolve) => setTimeout(resolve, 20)));
    const view = render({ consented: false, onEnableVoice: onEnable, photos: [photo("p1", "photo-1", "2026-11-01T10:00:00Z")] });
    click(button(view.container, "Ativar anotações de voz")!);
    const checkbox = view.container.querySelector<HTMLInputElement>("input[type=checkbox]")!;
    expect(checkbox.labels?.[0]?.textContent).toContain("Permitir anotações de voz (opcional)");
    click(checkbox);
    const authorize = button(view.container, "Autorizar anotações de voz")!;
    click(authorize); click(authorize);
    await waitFor(() => onEnable.mock.calls.length > 0, "authorization");
    expect(onEnable).toHaveBeenCalledTimes(1);
    await settle(8);
    // The page flips the consent once the server confirmed it; the section then confirms it and still waits for a press.
    view.rerender(createElement(PhotoVoiceNotes, { ...view.props, consented: true, photos: [photo("p1", "photo-1", "2026-11-01T10:00:00Z")] }));
    expect(view.text()).toContain("Anotações de voz ativadas. O microfone só será solicitado quando você iniciar uma anotação.");
    expect(view.control()).toBeDefined();
    expect(getUserMedia).not.toHaveBeenCalled();
  });

  it("keeps the opt-in open and explains in Portuguese when the authorization cannot be recorded", async () => {
    const view = render({ consented: false, onEnableVoice: vi.fn(async () => { throw new Error("pq: connection refused"); }) });
    click(button(view.container, "Ativar anotações de voz")!);
    click(view.container.querySelector<HTMLInputElement>("input[type=checkbox]")!);
    click(button(view.container, "Autorizar anotações de voz")!);
    await waitFor(() => view.container.querySelector("[role=alert]") !== null, "failure alert");
    expect(view.container.querySelector("[role=alert]")!.textContent).toMatch(/Não foi possível registrar a autorização de voz\. Fotos e vídeos continuam disponíveis/);
    expect(view.text()).not.toContain("connection refused");
  });

  it("IT-044 shows the photos of the room as fixed thumbnails and targets the chosen one", async () => {
    getUserMedia.mockResolvedValue(microphone());
    const view = render({ photos: [photo("p1", "photo-1", "2026-11-01T10:00:00Z"), photo("p2", "photo-2", "2026-11-01T10:05:00Z")] });
    expect(view.text()).toContain("Anotações de voz · Sala");
    expect(view.radios()).toHaveLength(2);
    expect(view.radios().map((radio) => radio.checked)).toEqual([false, true]);
    expect(view.container.querySelector("img")!.getAttribute("alt")).toBe("Foto 1 de 2 · Sala");
    click(view.radios()[0]);
    expect(view.radios().map((radio) => radio.checked)).toEqual([true, false]);
    pointer(view.control()!, "pointerdown");
    await waitFor(() => view.control()!.getAttribute("aria-label") === "Encerrar anotação de voz", "recording");
    clock.value = 900;
    pointer(window, "pointerup");
    await waitFor(() => uploads.upload.mock.calls.length === 1, "upload");
    expect(uploads.upload.mock.calls[0][0].target).toEqual({ kind: "PHOTO", photoDraftId: "p1", mediaId: "photo-1" });
  });

  it("IT-054 / IT-051 locks photo selection while a note is recorded and releases it when it ends", async () => {
    getUserMedia.mockResolvedValue(microphone());
    const view = render({ photos: [photo("p1", "photo-1", "2026-11-01T10:00:00Z"), photo("p2", "photo-2", "2026-11-01T10:05:00Z")] });
    pointer(view.control()!, "pointerdown");
    await waitFor(() => view.control()!.getAttribute("aria-label") === "Encerrar anotação de voz", "recording");
    expect(view.radios().every((radio) => radio.disabled || radio.closest("fieldset")?.disabled)).toBe(true);
    expect(view.text()).toContain("A foto fica fixa até você terminar a anotação.");
    clock.value = 900;
    pointer(window, "pointerup");
    await waitFor(() => !view.text().includes("A foto fica fixa"), "unlock");
    expect(view.container.querySelector("fieldset")!.disabled).toBe(false);
  });

  it("IT-045 / IT-046 / IT-056 keeps every note under its own photo, with no video timestamp, and counts them", () => {
    const items = [
      note("a", { kind: "PHOTO", photoDraftId: "p1", mediaId: "photo-1" }),
      note("b", { kind: "PHOTO", photoDraftId: "p1", mediaId: "photo-1" }),
      note("c", { kind: "PHOTO", photoDraftId: "p2" }),
      note("other-room", { kind: "PHOTO", photoDraftId: "p1" }, { requirementKey: "env:kitchen" })
    ];
    const view = render({ voice: { items, loaded: true, reload }, photos: [photo("p1", "photo-1", "2026-11-01T10:00:00Z"), photo("p2", "photo-2", "2026-11-01T10:05:00Z")] });
    const lists = [...view.container.querySelectorAll("ul[aria-label^='Anotações de Foto']")];
    expect(lists.map((list) => [list.getAttribute("aria-label"), list.querySelectorAll("li").length])).toEqual([["Anotações de Foto 1", 2], ["Anotações de Foto 2", 1]]);
    expect(view.text()).toContain("Foto 1 · 2 anotações");
    expect(view.text()).toContain("Foto 2 · 1 anotação");
    expect(view.text()).not.toMatch(/aproximado|Vídeo/);
    expect(view.container.querySelector('[data-note-id="other-room"]')).toBeNull();
  });

  it("IT-055 / IT-082 shows a note whose photo was replaced apart, never under the new photo", () => {
    const items = [note("old", { kind: "PHOTO", photoDraftId: "removed-photo", mediaId: "old-media" }, { state: "UNSENDABLE", noteId: undefined, statusMessage: "A foto desta anotação foi substituída ou removida." })];
    const view = render({ voice: { items, loaded: true, reload }, photos: [photo("p2", "photo-2", "2026-11-01T10:05:00Z")] });
    const orphan = view.container.querySelector('[data-note-id="old"]')!;
    expect(orphan.closest("section")!.textContent).toContain("Fotos substituídas ou removidas");
    expect(view.container.querySelector("ul[aria-label='Anotações de Foto 1']")).toBeNull();
    expect(view.text()).toContain("Foto 1 · 0 anotações");
  });

  it("IT-048 prompts to take a photo first when the room has none", () => {
    const view = render();
    expect(view.control()!.getAttribute("aria-disabled")).toBe("true");
    expect(view.text()).toContain("Selecione ou tire uma foto antes de gravar uma anotação.");
    pointer(view.control()!, "pointerdown");
    expect(getUserMedia).not.toHaveBeenCalled();
  });

  it("IT-049 / IT-076 blocks new recordings once the allowance is exhausted while keeping existing notes", () => {
    const items = [note("kept", { kind: "PHOTO", photoDraftId: "p1", mediaId: "photo-1" }), note("refused", { kind: "PHOTO", photoDraftId: "p1", mediaId: "photo-1" }, { state: "UNSENDABLE", failureCode: "LIMIT_REACHED", noteId: undefined, statusMessage: "O limite de uso de voz foi atingido." })];
    const view = render({ voice: { items, loaded: true, reload }, photos: [photo("p1", "photo-1", "2026-11-01T10:00:00Z")] });
    expect(view.control()!.getAttribute("aria-disabled")).toBe("true");
    expect(view.text()).toContain("O limite de anotações de voz foi atingido. Fotos e vídeos continuam disponíveis.");
    expect(view.container.querySelectorAll("li[data-note-id]")).toHaveLength(2);
  });

  it("IT-003 shows the effective limit and its scope", () => {
    const view = render({ policy: { inspectionNoteLimit: 20, inspectionAudioLimitMs: 1_800_000 }, photos: [photo("p1", "photo-1", "2026-11-01T10:00:00Z")] });
    expect(view.text()).toContain("Limite desta vistoria: até 20 anotações e 30 min de áudio. Cada anotação tem no máximo 60 segundos.");
  });

  it("IT-072 / IT-075 / IT-083 the review summary names pending and unsendable notes and hides itself when there are none", () => {
    const labelFor = (key: string) => key === "env:living" ? "Sala" : "Cozinha";
    const empty = mount(createElement(VoiceSubmissionSummary, { items: [], online: true, labelFor }));
    expect(empty.container.textContent).toBe("");
    const items = [
      note("ok", { kind: "PHOTO", photoDraftId: "p1", mediaId: "photo-1" }),
      note("pending", { kind: "PHOTO", photoDraftId: "p1", mediaId: "photo-1" }, { state: "PENDING_UPLOAD", noteId: undefined }),
      note("bad", { kind: "PHOTO", photoDraftId: "p1" }, { state: "UNSENDABLE", noteId: undefined, requirementKey: "env:kitchen", statusMessage: "O limite de uso de voz foi atingido." })
    ];
    const view = mount(createElement(VoiceSubmissionSummary, { items, online: true, labelFor }));
    const text = view.container.textContent ?? "";
    expect(text).toContain("3 anotações · 1 recebida · 1 pendente · 1 sem envio possível");
    expect(text).toContain("Sala: Envio pendente");
    expect(text).toContain("Cozinha: O limite de uso de voz foi atingido.");
    expect(text).toContain("As fotos e os vídeos não esperam pelas anotações.");
    expect(text).toContain("serão removidas deste dispositivo e não serão enviadas");
  });

  it("the opt-in for a room clip names clips and keeps the same separate authorization", () => {
    const view = mount(createElement(VoiceOptIn, { subject: "clipe", onEnable: async () => undefined }));
    expect(view.container.textContent).toContain("explicar cada clipe");
  });
});
