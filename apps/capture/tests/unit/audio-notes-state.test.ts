import { describe, expect, it } from "vitest";
import type { AudioDraft } from "@/pwa/audio-drafts";
import { VoiceFlowError, describeAnchor, describeNote, microphoneFailure, presentVoiceError, savedNoteMessage, summarizeNotes, voiceBlockReason, waitsForPhoto, type ServerNote } from "@/pwa/audio-notes-state";

const base: AudioDraft = { id: "note-1", responsibilityId: "responsibility-1", requirementKey: "env:living", disclosureVersion: "pt-BR-1", consentVersion: "voice-v1", target: { kind: "PHOTO", photoDraftId: "photo-draft-1" }, durationMs: 800, contentType: "audio/webm", sizeBytes: 5, sha256: "a".repeat(64), state: "PENDING_UPLOAD", createdAt: "2026-11-01T00:00:00Z" };
const server = (overrides: Partial<ServerNote> = {}): ServerNote => ({ id: "server-1", environmentId: "env", requirementKey: "env:living", targetKind: "PHOTO", photoMediaId: "photo-1", videoAttemptId: null, startMs: null, endMs: null, durationMs: 800, status: "COMPLETED", anchorStatus: "VALID", highlighted: false, version: 1, currentRevisionId: "r1", textConfirmed: false, noSpeech: false, failureCode: null, audioAvailability: "DELETED", audioDeletionStatus: "DELETED", revisions: [], ...overrides });

describe("voice note presentation", () => {
  it("IT-023 maps microphone failures to safe Portuguese that keeps visual capture available", () => {
    expect(microphoneFailure(new DOMException("Permission denied", "NotAllowedError")).message).toMatch(/acesso ao microfone foi negado.*Fotos e vídeos continuam disponíveis/);
    expect(microphoneFailure(new DOMException("x", "NotFoundError")).message).toMatch(/Nenhum microfone/);
    expect(microphoneFailure(new DOMException("x", "NotReadableError")).message).toMatch(/ocupado ou indisponível/);
    expect(microphoneFailure(new Error("boom")).message).not.toMatch(/boom/);
  });

  it("IT-093 never relays library or provider text to the interface", () => {
    expect(presentVoiceError(new Error("pq: relation transcription.audio_notes does not exist"))).toBe("Não foi possível concluir a operação com a anotação. Tente novamente.");
    expect(presentVoiceError({ code: "SOMETHING_NEW", message: "internal stack trace" })).not.toMatch(/stack/);
    expect(presentVoiceError(new VoiceFlowError("X", "Mensagem segura."))).toBe("Mensagem segura.");
  });

  it("IT-020 / IT-022 / IT-077 gives each stable server code its own Portuguese explanation", () => {
    expect(presentVoiceError({ code: "CONSENT_REQUIRED" })).toMatch(/Ative as anotações de voz/);
    expect(presentVoiceError({ code: "LIMIT_REACHED" })).toMatch(/limite de uso de voz.*fotos e vídeos continuam/);
    expect(presentVoiceError({ code: "SESSION_EXPIRED" })).toMatch(/sessão de captura expirou.*continua salva neste dispositivo/);
    expect(presentVoiceError({ code: "CONFLICT" })).toMatch(/alterada em outro lugar/);
    expect(presentVoiceError({ code: "NETWORK_ERROR" })).toMatch(/Sem conexão/);
  });

  it("IT-098 distinguishes awaiting upload, queued, transcribing, ready, no speech, failed and expired", () => {
    const status = (item: Parameters<typeof describeNote>[0], online = true) => describeNote(item, online).label;
    expect(status({ draft: base })).toBe("Aguardando o envio da foto; o áudio fica neste dispositivo");
    expect(status({ draft: { ...base, target: { kind: "PHOTO", mediaId: "photo-1" } } })).toBe("Envio pendente");
    expect(status({ draft: { ...base, target: { kind: "PHOTO", mediaId: "photo-1" } } }, false)).toBe("Salva neste dispositivo; envio pendente até a conexão voltar");
    const received = { ...base, state: "RECEIVED" as const, noteId: "server-1" };
    expect(status({ draft: received, note: server({ status: "QUEUED" }) })).toBe("Na fila de transcrição");
    expect(status({ draft: received, note: server({ status: "PROCESSING" }) })).toBe("Transcrevendo");
    expect(status({ draft: received, note: server({ status: "COMPLETED" }) })).toBe("Texto pronto para revisão");
    expect(status({ draft: received, note: server({ status: "COMPLETED", textConfirmed: true }) })).toBe("Texto confirmado");
    expect(status({ draft: received, note: server({ status: "COMPLETED", noSpeech: true }) })).toMatch(/Nenhuma fala foi identificada/);
    expect(status({ draft: received, note: server({ status: "FAILED" }) })).toBe("Transcrição não concluída");
    expect(status({ draft: received, note: server({ status: "EXPIRED" }) })).toBe("Anotação expirada");
    expect(status({ draft: received, note: server({ status: "CANCELED" }) })).toBe("Anotação descartada");
  });

  it("IT-074 / IT-083 shows unsendable and failed notes distinctly instead of one generic spinner", () => {
    const items = [
      { draft: { ...base, id: "a", state: "UNSENDABLE" as const, statusMessage: "O áudio salvo neste dispositivo está incompleto." } },
      { draft: { ...base, id: "b", state: "FAILED" as const } },
      { draft: { ...base, id: "c", state: "RECEIVED" as const } },
      { draft: { ...base, id: "d" } }
    ];
    expect(describeNote(items[0], true)).toMatchObject({ tone: "danger", settled: true, label: "O áudio salvo neste dispositivo está incompleto." });
    expect(describeNote(items[1], true).label).toMatch(/Falha no envio; áudio mantido neste dispositivo/);
    expect(summarizeNotes(items)).toEqual({ total: 4, received: 1, pending: 1, failed: 1, unsendable: 1 });
  });

  it("IT-075 reports no pending status when no note exists", () => {
    expect(summarizeNotes([])).toEqual({ total: 0, received: 0, pending: 0, failed: 0, unsendable: 0 });
  });

  it("IT-032 / IT-045 labels video notes with an approximate interval and photo notes with no timestamp", () => {
    expect(describeAnchor({ ...base, target: { kind: "VIDEO", attemptId: "a", startMs: 1_200, endMs: 3_400, timeBasis: "CAPTURE_CLOCK" } })).toBe("Vídeo · 1–4 s (aproximado)");
    expect(describeAnchor(base)).toBe("Foto");
    expect(describeAnchor(base)).not.toMatch(/\d/);
  });

  it("IT-048 / IT-061 explains why the control cannot start", () => {
    expect(voiceBlockReason({ enabled: false, hasTarget: true, kind: "PHOTO", limitReached: false })).toMatch(/Ative as anotações de voz/);
    expect(voiceBlockReason({ enabled: true, hasTarget: false, kind: "PHOTO", limitReached: false })).toMatch(/Selecione ou tire uma foto/);
    expect(voiceBlockReason({ enabled: true, hasTarget: true, kind: "PHOTO", limitReached: true })).toMatch(/limite de anotações de voz/);
    expect(voiceBlockReason({ enabled: true, hasTarget: true, kind: "PHOTO", limitReached: false })).toBeUndefined();
  });

  it("IT-033 / IT-036 / IT-062 uses one explanation for each way a note can end", () => {
    expect(savedNoteMessage("limit", "duration")).toMatch(/60 segundos/);
    expect(savedNoteMessage("limit", "size")).toMatch(/5 MB/);
    expect(savedNoteMessage("device")).toMatch(/microfone foi interrompido/);
    expect(savedNoteMessage("focus")).toMatch(/perdeu o foco/);
    expect(savedNoteMessage("background")).toMatch(/primeiro plano/);
    expect(savedNoteMessage("video-end")).toMatch(/junto com o vídeo/);
  });

  it("waits for the photo only while its media identity is unknown", () => {
    expect(waitsForPhoto(base)).toBe(true);
    expect(waitsForPhoto({ ...base, target: { kind: "PHOTO", mediaId: "photo-1" } })).toBe(false);
    expect(waitsForPhoto({ ...base, state: "RECEIVED" })).toBe(false);
  });
});
