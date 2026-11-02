import "fake-indexeddb/auto";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { button, click, createElement, mount, setValue, settle, silenceActWarnings, unmountAll, waitFor } from "./support/dom";
import { VoiceNoteItems } from "../../app/capture/[linkToken]/voice-note-items";
import { listAudioDrafts, saveAudioDraft, type AudioDraft } from "@/pwa/audio-drafts";
import type { NoteItem, ServerNote } from "@/pwa/audio-notes-state";

const api = vi.hoisted(() => ({ graphql: vi.fn(), upload: vi.fn() }));
vi.mock("@/graphql/client", () => ({ graphql: api.graphql }));
vi.mock("@/pwa/audio-uploads", () => ({ uploadAudioDraft: api.upload }));

const responsibilityId = "responsibility-1";
const draft = (overrides: Partial<AudioDraft> = {}): AudioDraft => ({ id: "note-1", responsibilityId, requirementKey: "env:living", disclosureVersion: "pt-BR-1", consentVersion: "voice-v1", target: { kind: "PHOTO", photoDraftId: "photo-draft-1", mediaId: "photo-1" }, durationMs: 800, contentType: "audio/webm", sizeBytes: 5, sha256: "a".repeat(64), state: "RECEIVED", noteId: "server-1", createdAt: "2026-11-01T00:00:00Z", ...overrides });
const revision = (text: string, number = 1) => ({ id: `r${number}`, revisionNumber: number, text, kind: "MODEL", createdAt: "2026-11-01T00:00:00Z" });
const serverNote = (overrides: Partial<ServerNote> = {}): ServerNote => ({ id: "server-1", environmentId: "env", requirementKey: "env:living", targetKind: "PHOTO", photoMediaId: "photo-1", videoAttemptId: null, startMs: null, endMs: null, durationMs: 800, status: "COMPLETED", anchorStatus: "VALID", highlighted: false, version: 3, currentRevisionId: "r1", textConfirmed: false, noSpeech: false, failureCode: null, audioAvailability: "DELETED", audioDeletionStatus: "DELETED", revisions: [revision("cinco pratos")], ...overrides });
const completed = (note: Partial<ServerNote> = {}): NoteItem => ({ draft: draft(), note: serverNote(note) });

const reload = vi.fn(async () => undefined);
function render(items: NoteItem[], online = true) {
  const view = mount(createElement(VoiceNoteItems, { items, online, reload, label: "Anotações de Foto 1", title: "Anotações da foto 1", emptyText: "Nenhuma anotação nesta foto.", titleFor: (_item: NoteItem, index: number) => `Anotação ${index + 1}` }));
  const text = () => view.container.textContent ?? "";
  const notice = () => view.container.querySelector(".capture-voice-notes__notice")?.textContent ?? "";
  return { ...view, text, notice, textarea: (label: string) => [...view.container.querySelectorAll<HTMLTextAreaElement>("textarea")].find((area) => area.labels?.[0]?.textContent?.includes(label)) };
}
const payload = (call: number) => (api.graphql.mock.calls[call][1] as { input: Record<string, unknown> }).input;
const ok = (field: string) => ({ [field]: { note: { id: "server-1", status: "COMPLETED", version: 4 }, userErrors: [], clientMutationId: "x" } });

describe("VoiceNoteItems", () => {
  beforeEach(async () => {
    silenceActWarnings();
    api.graphql.mockReset(); api.upload.mockReset(); reload.mockClear();
    await new Promise<void>((resolve, reject) => { const request = indexedDB.deleteDatabase("inspection-audio-notes-v1"); request.onsuccess = () => resolve(); request.onerror = () => reject(request.error); });
  });
  afterEach(() => { unmountAll(); vi.restoreAllMocks(); });

  it("IT-084 shows the original text, the current text, the anchor and whether audio is still available", () => {
    const view = render([{ draft: draft(), note: serverNote({ revisions: [revision("cinco pratos"), revision("seis pratos", 2)], currentRevisionId: "r2", version: 4 }) }]);
    expect(view.text()).toContain("Anotação 1");
    expect(view.text()).toContain("Texto original: cinco pratos");
    expect(view.text()).toContain("Texto atual: seis pratos");
    expect(view.text()).toContain("Áudio original indisponível; o texto pode ser revisado.");
    expect(view.text()).toContain("Texto pronto para revisão");
  });

  it("IT-089 / IT-104 shows processing, not a blank editable transcript, before the text exists", () => {
    const view = render([{ draft: draft(), note: serverNote({ status: "PROCESSING", revisions: [], currentRevisionId: null, audioAvailability: "AVAILABLE" }) }]);
    expect(view.text()).toContain("Transcrevendo");
    expect(view.text()).toContain("A transcrição ainda está em processamento. O áudio original não é exibido como texto concluído.");
    expect(view.textarea("Corrigir texto")).toBeUndefined();
    expect(view.text()).toContain("Áudio temporário ainda disponível para processamento.");
  });

  it("IT-085 saves a correction as a human edit against the current version, without another transcription", async () => {
    api.graphql.mockResolvedValueOnce({ reviseAudioNote: { note: { id: "server-1", status: "COMPLETED", version: 4, currentRevisionId: "r2", revisions: [] }, userErrors: [], clientMutationId: "x" } });
    const view = render([completed()]);
    setValue(view.textarea("Corrigir texto")!, "  seis pratos  ");
    click(button(view.container, "Salvar correção")!);
    await waitFor(() => api.graphql.mock.calls.length === 1, "revise call");
    expect(payload(0)).toMatchObject({ noteId: "server-1", text: "seis pratos", revisionKind: "HUMAN_EDIT", expectedVersion: 3 });
    expect(String(payload(0).clientMutationId)).toMatch(/^note-1:revise:3:/);
    await waitFor(() => view.notice() === "Correção salva sem solicitar outra transcrição.", "success notice");
    expect(reload).toHaveBeenCalled();
  });

  it("IT-088 / IT-090 rejects an empty or oversized correction with a Portuguese message before any request", () => {
    const view = render([completed()]);
    expect(view.text()).toContain("12 de 12.000 caracteres");
    setValue(view.textarea("Corrigir texto")!, "   ");
    expect(view.text()).toContain("Digite o texto da anotação antes de salvar.");
    expect(button(view.container, "Salvar correção")!.disabled).toBe(true);
    setValue(view.textarea("Corrigir texto")!, "a".repeat(12_001));
    expect(view.text()).toContain("O texto pode ter até 12.000 caracteres.");
    expect(view.text()).toContain("12.001 de 12.000 caracteres");
    expect(button(view.container, "Salvar correção")!.disabled).toBe(true);
    click(button(view.container, "Salvar correção")!);
    expect(api.graphql).not.toHaveBeenCalled();
  });

  it("IT-092 reports a stale save in Portuguese, reloads the latest text and keeps what the participant typed", async () => {
    api.graphql.mockResolvedValueOnce({ reviseAudioNote: { note: null, userErrors: [{ code: "CONFLICT", field: "expectedVersion", message: "stale", correlationId: null }], clientMutationId: "x" } });
    const view = render([completed()]);
    setValue(view.textarea("Corrigir texto")!, "seis pratos");
    click(button(view.container, "Salvar correção")!);
    await waitFor(() => /alterada em outro lugar/.test(view.notice()), "conflict notice");
    expect(view.notice()).not.toMatch(/stale/);
    expect(reload).toHaveBeenCalled();
    expect(view.textarea("Corrigir texto")!.value).toBe("seis pratos");
  });

  it("IT-093 does not claim a correction was saved when the request is interrupted", async () => {
    api.graphql.mockRejectedValueOnce({ code: "NETWORK_ERROR", message: "Não foi possível conectar à API." });
    const view = render([completed()]);
    setValue(view.textarea("Corrigir texto")!, "seis pratos");
    click(button(view.container, "Salvar correção")!);
    await waitFor(() => /Sem conexão com o servidor/.test(view.notice()), "interruption notice");
    expect(view.notice()).not.toMatch(/salva sem/);
    expect(view.text()).toContain("Texto atual: cinco pratos");
  });

  it("IT-086 / IT-094 asks before discarding and sends one discard even if the button is pressed repeatedly", async () => {
    api.graphql.mockResolvedValueOnce(ok("discardAudioNote"));
    const view = render([completed()]);
    click(button(view.container, "Descartar anotação")!);
    expect(view.text()).toContain("Descartar esta anotação? Ela deixará de ser usada em sugestões.");
    click(button(view.container, "Manter anotação")!);
    expect(view.text()).not.toContain("Descartar esta anotação?");
    click(button(view.container, "Descartar anotação")!);
    const confirm = button(view.container, "Sim, descartar")!;
    click(confirm); click(confirm);
    await waitFor(() => /Anotação descartada e excluída de novas sugestões\./.test(view.notice()), "discard notice");
    expect(api.graphql).toHaveBeenCalledTimes(1);
    expect(payload(0)).toMatchObject({ noteId: "server-1", expectedVersion: 3 });
  });

  it("IT-124 shows a discarded note as closed, with no highlight or discard action", () => {
    const view = render([completed({ status: "CANCELED", highlighted: true })]);
    expect(view.text()).toContain("Anotação descartada");
    expect(view.text()).toContain("Esta anotação não será usada.");
    expect(button(view.container, "Destacar anotação")).toBeUndefined();
    expect(button(view.container, "Descartar anotação")).toBeUndefined();
    expect(view.text()).not.toContain("Destacada");
  });

  it("IT-113 / IT-122 toggles the highlight as an explicit, individually identifiable mark", async () => {
    api.graphql.mockResolvedValueOnce({ setAudioNoteHighlight: { note: { id: "server-1", highlighted: true, version: 4 }, userErrors: [], clientMutationId: "x" } });
    const view = render([completed()]);
    const highlight = button(view.container, "Destacar anotação")!;
    expect(highlight.getAttribute("aria-pressed")).toBe("false");
    click(highlight);
    await waitFor(() => api.graphql.mock.calls.length === 1, "highlight call");
    expect(payload(0)).toMatchObject({ noteId: "server-1", highlighted: true, expectedVersion: 3 });
    await settle(3);
    unmountAll();
    api.graphql.mockResolvedValueOnce({ setAudioNoteHighlight: { note: { id: "server-1", highlighted: false, version: 5 }, userErrors: [], clientMutationId: "x" } });
    const marked = render([completed({ highlighted: true, version: 4 })]);
    expect(marked.text()).toContain("Destacada");
    expect(button(marked.container, "Destacar anotação")!.getAttribute("aria-pressed")).toBe("true");
    click(button(marked.container, "Destacar anotação")!);
    await waitFor(() => api.graphql.mock.calls.length === 2, "unhighlight call");
    expect(payload(1)).toMatchObject({ highlighted: false, expectedVersion: 4 });
  });

  it("IT-117 / IT-118 keeps the highlight available before any transcript and marks several notes separately", () => {
    const view = render([
      { draft: draft({ id: "a" }), note: serverNote({ id: "s-a", status: "QUEUED", revisions: [], currentRevisionId: null, highlighted: true }) },
      { draft: draft({ id: "b" }), note: serverNote({ id: "s-b", highlighted: true }) },
      { draft: draft({ id: "c" }), note: serverNote({ id: "s-c" }) }
    ]);
    expect(view.container.querySelectorAll("li[data-note-id]")).toHaveLength(3);
    expect([...view.container.querySelectorAll("li[data-note-id]")].map((row) => row.textContent?.includes("Destacada"))).toEqual([true, true, false]);
    expect(button(view.container.querySelector('[data-note-id="a"]')!, "Destacar anotação")).toBeDefined();
  });

  it("IT-121 keeps the previous mark and says the highlight failed", async () => {
    api.graphql.mockResolvedValueOnce({ setAudioNoteHighlight: { note: null, userErrors: [{ code: "INVALID_STATE", field: "noteId", message: "x", correlationId: null }], clientMutationId: "x" } });
    const view = render([completed()]);
    click(button(view.container, "Destacar anotação")!);
    await waitFor(() => /não pode mais ser alterada/.test(view.notice()), "failure notice");
    expect(button(view.container, "Destacar anotação")!.getAttribute("aria-pressed")).toBe("false");
  });

  it("IT-095 / IT-102 offers retry and a typed declaration, identified as the participant's own, when transcription failed", async () => {
    api.graphql.mockResolvedValueOnce({ reviseAudioNote: { note: { id: "server-1", status: "FAILED", version: 4, currentRevisionId: "r1", revisions: [] }, userErrors: [], clientMutationId: "x" } });
    const view = render([{ draft: draft(), note: serverNote({ status: "FAILED", revisions: [], currentRevisionId: null, audioAvailability: "AVAILABLE", failureCode: "MODEL_UNAVAILABLE" }) }]);
    expect(view.text()).toContain("A transcrição não foi concluída. Tente novamente ou escreva a descrição abaixo.");
    expect(button(view.container, "Tentar transcrição novamente")).toBeDefined();
    expect(view.text()).toContain("Seu texto será identificado como declaração sua, não como transcrição automática.");
    setValue(view.textarea("Escrever a descrição")!, "tem cinco pratos");
    click(button(view.container, "Salvar minha descrição")!);
    await waitFor(() => api.graphql.mock.calls.length === 1, "declaration call");
    expect(payload(0)).toMatchObject({ text: "tem cinco pratos", revisionKind: "PARTICIPANT_DECLARATION" });
    expect(view.text()).not.toContain("MODEL_UNAVAILABLE");
  });

  it("IT-101 offers no retry once the original audio is gone, but still allows the typed description", () => {
    const view = render([{ draft: draft(), note: serverNote({ status: "FAILED", revisions: [], currentRevisionId: null, audioAvailability: "DELETED" }) }]);
    expect(button(view.container, "Tentar transcrição novamente")).toBeUndefined();
    expect(view.textarea("Escrever a descrição")).toBeDefined();
  });

  it("IT-034 / IT-081 explains a pending or invalid video anchor", () => {
    const pending = render([{ draft: draft({ target: { kind: "VIDEO", attemptId: "a", startMs: 1000, endMs: 2000, timeBasis: "CAPTURE_CLOCK" } }), note: serverNote({ anchorStatus: "PENDING", targetKind: "VIDEO_INTERVAL" }) }]);
    expect(pending.text()).toContain("A posição no vídeo será conferida quando o clipe for recebido.");
    unmountAll();
    const invalid = render([{ draft: draft(), note: serverNote({ anchorStatus: "INVALID" }) }]);
    expect(invalid.text()).toContain("O trecho informado não coincide com o vídeo; esta anotação não será usada em sugestões.");
  });

  it("IT-074 / IT-083 lists every local note with its own status and lets an unsendable one be discarded for good", async () => {
    await saveAudioDraft(draft({ id: "bad", state: "PENDING_UPLOAD", noteId: undefined, audio: new Blob(["voice"]) }));
    const items: NoteItem[] = [
      { draft: draft({ id: "bad", state: "UNSENDABLE", noteId: undefined, statusMessage: "O áudio salvo neste dispositivo está incompleto. Descarte esta anotação e grave novamente." }) },
      { draft: draft({ id: "failed", state: "FAILED", noteId: undefined }) },
      { draft: draft({ id: "pending", state: "PENDING_UPLOAD", noteId: undefined, target: { kind: "PHOTO", photoDraftId: "photo-draft-1", mediaId: "photo-1" } }) },
      { draft: draft({ id: "waiting", state: "PENDING_UPLOAD", noteId: undefined, target: { kind: "PHOTO", photoDraftId: "photo-draft-9" } }) }
    ];
    const view = render(items);
    const rows = [...view.container.querySelectorAll("li[data-note-id]")].map((row) => row.textContent ?? "");
    expect(rows[0]).toContain("incompleto");
    expect(rows[1]).toContain("Falha no envio; áudio mantido neste dispositivo");
    expect(rows[2]).toContain("Envio pendente");
    expect(rows[3]).toContain("Aguardando o envio da foto");
    expect(button(view.container.querySelector('[data-note-id="waiting"]')!, "Tentar enviar agora")).toBeUndefined();
    expect(button(view.container.querySelector('[data-note-id="bad"]')!, "Tentar enviar agora")).toBeUndefined();
    click(button(view.container.querySelector('[data-note-id="bad"]')!, "Descartar anotação")!);
    await waitFor(() => /descartada deste dispositivo/.test(view.notice()), "local discard notice");
    expect(await listAudioDrafts(responsibilityId)).toEqual([]);
  });

  it("IT-071 retries a pending upload on request, once, when online; and not while offline", async () => {
    api.upload.mockResolvedValue({ ...draft(), state: "RECEIVED" });
    const item: NoteItem = { draft: draft({ state: "FAILED", noteId: undefined }) };
    const offline = render([item], false);
    expect(button(offline.container, "Tentar enviar agora")!.disabled).toBe(true);
    unmountAll();
    const online = render([item], true);
    const retry = button(online.container, "Tentar enviar agora")!;
    click(retry); click(retry);
    await waitFor(() => api.upload.mock.calls.length >= 1, "upload call");
    expect(api.upload).toHaveBeenCalledTimes(1);
  });

  it("IT-075 shows the empty message and no false pending status when there are no notes", () => {
    const view = render([]);
    expect(view.text()).toContain("Nenhuma anotação nesta foto.");
    expect(view.container.querySelector("li")).toBeNull();
  });

  it("IT-091 / IT-106 never displays text of a note the server did not return", () => {
    const view = render([{ draft: draft() }]);
    expect(view.text()).toContain("Áudio recebido. O status será atualizado quando houver conexão");
    expect(view.text()).not.toContain("Revisar anotação");
    expect(view.text()).not.toContain("cinco pratos");
  });
});
