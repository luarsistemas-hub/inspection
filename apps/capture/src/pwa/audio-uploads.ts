import { graphql } from "@/graphql/client";
import { CaptureAudioNoteDocument, CompleteAudioNoteUploadDocument, CreateAudioNoteUploadDocument } from "@/graphql/generated";
import { throwOnUserErrors } from "@/pwa/mutation-errors";
import { isReadyCaptureDraft, loadDraftsForResponsibility } from "./drafts";
import { listAudioDrafts, loadAudioDraft, updateAudioDraft, type AudioDraft } from "./audio-drafts";
import { VoiceFlowError, presentVoiceError, sessionVoiceCodes, terminalVoiceCodes, voiceErrorCode } from "./audio-notes-state";

type UploadIntent = { url: string; method: string; headers: Record<string, string>; expiresAt: string };

const inFlight = new Map<string, Promise<AudioDraft>>();

/** Serializes one note across tabs when Web Locks exist; the stable clientNoteId makes the server idempotent either way. */
async function withNoteLock<T>(id: string, fallback: () => Promise<T>, run: () => Promise<T>): Promise<T> {
  const locks = typeof navigator === "undefined" ? undefined : (navigator as Navigator & { locks?: LockManager }).locks;
  if (!locks) return run();
  const outcome = await locks.request(`inspection-audio-upload:${id}`, { ifAvailable: true }, async (lock) => lock ? { value: await run() } : undefined);
  return outcome ? outcome.value : fallback();
}

type PhotoResolution = { state: "RESOLVED"; mediaId: string } | { state: "WAITING" } | { state: "MISSING" };

/** A photo note is sent only once its own photo has a durable server media ID; it is never moved to another photo. */
async function resolvePhoto(draft: AudioDraft): Promise<PhotoResolution> {
  if (draft.target.kind !== "PHOTO") return { state: "WAITING" };
  if (draft.target.mediaId) return { state: "RESOLVED", mediaId: draft.target.mediaId };
  const photoDraftId = draft.target.photoDraftId;
  const photos = await loadDraftsForResponsibility(draft.responsibilityId).catch(() => []);
  const photo = photos.find((item) => item.id === photoDraftId);
  if (!photo) return { state: "MISSING" };
  if (!photo.mediaId || !isReadyCaptureDraft(photo)) return { state: "WAITING" };
  return { state: "RESOLVED", mediaId: photo.mediaId };
}

async function markUnsendable(draft: AudioDraft, code: string, message: string): Promise<VoiceFlowError> {
  await updateAudioDraft({ ...draft, state: "UNSENDABLE", failureCode: code, statusMessage: message }).catch(() => undefined);
  return new VoiceFlowError(code, message);
}

/** After a lost acknowledgement the note may already be durable; confirm with the server instead of creating a second job. */
async function reconcileReceipt(draft: AudioDraft): Promise<AudioDraft | undefined> {
  if (!draft.noteId) return undefined;
  try {
    const existing = (await graphql(CaptureAudioNoteDocument, { id: draft.noteId })).audioNote;
    if (!existing || existing.status === "UPLOADING") return undefined;
    const received: AudioDraft = { ...draft, state: "RECEIVED", audio: undefined, failureCode: undefined, statusMessage: "Áudio recebido para processamento." };
    await updateAudioDraft(received);
    return received;
  } catch { return undefined; }
}

async function performUpload(source: AudioDraft): Promise<AudioDraft> {
  let draft = await loadAudioDraft(source.id, source.responsibilityId) ?? source;
  if (draft.state === "RECEIVED") return draft;
  if (draft.state === "UNSENDABLE") throw new VoiceFlowError(draft.failureCode ?? "UNSENDABLE", draft.statusMessage ?? "Esta anotação não pode ser enviada.");
  if (!draft.audio || draft.audio.size !== draft.sizeBytes) throw await markUnsendable(draft, "LOCAL_AUDIO_UNAVAILABLE", "O áudio salvo neste dispositivo está incompleto. Descarte esta anotação e grave novamente.");

  let photoMediaId: string | null = null;
  if (draft.target.kind === "PHOTO") {
    const photo = await resolvePhoto(draft);
    if (photo.state === "WAITING") return draft;
    if (photo.state === "MISSING") throw await markUnsendable(draft, "NOT_FOUND", "A foto desta anotação foi substituída ou removida. A anotação não foi enviada e não será anexada a outra foto.");
    photoMediaId = photo.mediaId;
    if (draft.target.mediaId !== photoMediaId) {
      draft = { ...draft, target: { ...draft.target, mediaId: photoMediaId } };
      await updateAudioDraft(draft);
    }
  }
  const target = draft.target;

  try {
    const result = await graphql(CreateAudioNoteUploadDocument, { input: {
      clientNoteId: draft.id,
      requirementKey: draft.requirementKey,
      photoMediaId,
      videoAttemptId: target.kind === "VIDEO" ? target.attemptId : null,
      startMs: target.kind === "VIDEO" ? target.startMs : null,
      endMs: target.kind === "VIDEO" ? target.endMs : null,
      timeBasis: target.kind === "VIDEO" ? target.timeBasis : null,
      consentVersion: draft.consentVersion,
      disclosureVersion: draft.disclosureVersion,
      contentType: draft.contentType,
      contentSha256: draft.sha256,
      sizeBytes: draft.sizeBytes,
      durationMs: draft.durationMs,
      clientMutationId: `${draft.id}:create`
    } });
    throwOnUserErrors(result.createAudioNoteUpload);
    if (!result.createAudioNoteUpload.noteId || !result.createAudioNoteUpload.upload) throw new VoiceFlowError("UPLOAD_NOT_STARTED", "Não foi possível iniciar o envio da anotação.");
    const noteId = String(result.createAudioNoteUpload.noteId);
    const intent = result.createAudioNoteUpload.upload as UploadIntent;
    const proof = intent.headers?.["x-upload-proof"] ?? intent.headers?.["X-Upload-Proof"] ?? "";
    if (!proof || Date.parse(intent.expiresAt) <= Date.now()) throw new VoiceFlowError("UPLOAD_EXPIRED", "O acesso temporário para envio expirou. Tente novamente.");
    if (draft.noteId && draft.noteId !== noteId) throw new VoiceFlowError("NOTE_MISMATCH", "O servidor retornou uma anotação diferente da salva neste dispositivo.");
    await updateAudioDraft({ ...draft, noteId, state: "PENDING_UPLOAD" });
    // The proof identifies the upload to our API; it is not a storage request header.
    const uploadHeaders = new Headers(intent.headers);
    uploadHeaders.delete("x-upload-proof");
    let response: Response;
    try {
      response = await fetch(intent.url, { method: intent.method || "PUT", body: draft.audio, headers: uploadHeaders });
    } catch {
      throw new VoiceFlowError("UPLOAD_CONNECTION_FAILED", "Não foi possível conectar ao armazenamento da anotação. Ela continua salva neste dispositivo.");
    }
    if (!response.ok) throw new VoiceFlowError("UPLOAD_FAILED", "Não foi possível enviar a anotação. Ela continua salva neste dispositivo.");
    const completed = await graphql(CompleteAudioNoteUploadDocument, { input: { noteId, uploadProof: proof, clientMutationId: `${draft.id}:complete` } });
    throwOnUserErrors(completed.completeAudioNoteUpload);
    if (!completed.completeAudioNoteUpload.note) throw new VoiceFlowError("RECEIPT_MISSING", "O servidor ainda não confirmou o recebimento da anotação.");
    // The receipt is durable: this update drops the local bytes and keeps only the metadata needed to show the note.
    const received: AudioDraft = { ...draft, noteId, state: "RECEIVED", audio: undefined, failureCode: undefined, statusMessage: "Áudio recebido para processamento." };
    await updateAudioDraft(received);
    return received;
  } catch (error) {
    const code = voiceErrorCode(error);
    const latest = await loadAudioDraft(draft.id, draft.responsibilityId) ?? draft;
    if (code === "INVALID_STATE" || code === "CONFLICT") {
      const reconciled = await reconcileReceipt(latest);
      if (reconciled) return reconciled;
    }
    if (code && terminalVoiceCodes.has(code)) throw await markUnsendable(latest, code, presentVoiceError(error));
    const message = code && sessionVoiceCodes.has(code) ? presentVoiceError(error) : "O envio está pendente. A anotação continua salva neste dispositivo.";
    await updateAudioDraft({ ...latest, state: "FAILED", statusMessage: message }).catch(() => undefined);
    throw error;
  }
}

/** Uploads one locally retained note and removes its bytes only after durable server receipt; concurrent calls share one run. */
export function uploadAudioDraft(source: AudioDraft): Promise<AudioDraft> {
  const running = inFlight.get(source.id);
  if (running) return running;
  const run = withNoteLock(source.id, async () => await loadAudioDraft(source.id, source.responsibilityId) ?? source, () => performUpload(source)).finally(() => { inFlight.delete(source.id); });
  inFlight.set(source.id, run);
  return run;
}

/** Reconciles pending notes independently so one failed upload does not hide other notes. */
export async function resumeAudioDrafts(drafts: AudioDraft[]): Promise<Array<{ id: string; draft?: AudioDraft; error?: unknown }>> {
  return Promise.all(drafts.filter((draft) => draft.state === "PENDING_UPLOAD" || draft.state === "FAILED").map(async (draft) => {
    try { return { id: draft.id, draft: await uploadAudioDraft(draft) }; }
    catch (error) { return { id: draft.id, error }; }
  }));
}

/** Tries to send every pending note of a responsibility before it closes, bounded so visual submission is never held hostage. */
export async function flushAudioDrafts(responsibilityId: string, timeoutMs = 20_000): Promise<AudioDraft[]> {
  const drafts = await listAudioDrafts(responsibilityId).catch(() => []);
  const pending = drafts.filter((draft) => draft.state === "PENDING_UPLOAD" || draft.state === "FAILED");
  if (pending.length > 0) await Promise.race([resumeAudioDrafts(pending), new Promise((resolve) => setTimeout(resolve, timeoutMs))]);
  return (await listAudioDrafts(responsibilityId).catch(() => drafts)).filter((draft) => draft.state !== "RECEIVED");
}
