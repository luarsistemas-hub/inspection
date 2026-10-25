import { captureOperations, graphql } from "@/graphql/client";
import { throwOnUserErrors } from "@/pwa/mutation-errors";
import { withDraftLock } from "@/pwa/uploads";
import { loadVideoDraft, readVideoChunk, saveVideoDraft, type VideoDraft } from "./video-drafts";

const defaultPartSize = 5 * 1024 * 1024;
const receivedStatuses = new Set(["RECEIVED", "VERIFIED", "READY", "SCREENED", "REJECTED"]);

async function readPart(draft: VideoDraft, number: number, partSize: number): Promise<Blob> {
  const start = (number - 1) * partSize;
  const end = Math.min(draft.sizeBytes, start + partSize);
  const slices: Blob[] = [];
  let offset = 0;
  for (let sequence = 0; sequence < draft.chunkCount && offset < end; sequence += 1) {
    const chunk = await readVideoChunk(draft.id, sequence);
    if (!chunk) throw new Error("A gravação local está incompleta. Grave um novo vídeo.");
    const chunkEnd = offset + chunk.size;
    if (chunkEnd > start && offset < end) slices.push(chunk.slice(Math.max(0, start - offset), Math.min(chunk.size, end - offset)));
    offset = chunkEnd;
  }
  if (offset < end) throw new Error("A gravação local está incompleta. Grave um novo vídeo.");
  return new Blob(slices, { type: draft.contentType });
}

async function status(mediaId: string) {
  return (await graphql(captureOperations.videoStatus, { mediaId })).videoUploadStatus;
}

export async function uploadVideoDraft(source: VideoDraft, onProgress?: (completed: number, total: number) => void): Promise<VideoDraft> {
  return withDraftLock(source.id, async () => {
    let draft = await loadVideoDraft(source.id) ?? source;
    if (draft.state !== "SAVED" && draft.state !== "CONFIRMED" && draft.state !== "RECEIVED") throw new Error("Pare, finalize e revise o vídeo antes de enviar.");
    if (draft.state === "SAVED") { draft = { ...draft, state: "CONFIRMED" }; await saveVideoDraft(draft); }
    if (!draft.sha256 || draft.sizeBytes < 1) throw new Error("A gravação não tem integridade confirmada.");
    if (!draft.mediaId) {
      const result = await graphql(captureOperations.createUpload, { input: { requirementKey: draft.requirementKey ?? null, contentType: draft.contentType, sizeBytes: draft.sizeBytes, sha256: draft.sha256, attemptId: draft.id, clientMutationId: `${draft.id}:create` } });
      throwOnUserErrors(result.createMediaUpload);
      const upload = result.createMediaUpload.upload;
      if (!upload) throw new Error("Não foi possível iniciar o envio.");
      draft = { ...draft, mediaId: upload.mediaId, uploadId: upload.uploadId, partSizeBytes: upload.partSizeBytes || defaultPartSize };
      await saveVideoDraft(draft);
    }
    const mediaId = draft.mediaId;
    if (!mediaId) throw new Error("O servidor não retornou a mídia de vídeo.");
    const partSize = draft.partSizeBytes || defaultPartSize;
    const total = Math.ceil(draft.sizeBytes / partSize);
    if (total < 1) throw new Error("A gravação está vazia.");
    const remote = await status(mediaId);
    if (!receivedStatuses.has(remote.status)) {
      draft = { ...draft, parts: remote.confirmedParts.map((part) => ({ number: part.partNumber, etag: part.etag })) };
      await saveVideoDraft(draft);
      let pending = Array.from({ length: total }, (_, index) => index + 1).filter((number) => !draft.parts.some((part) => part.number === number && part.etag));
      while (pending.length) {
        const batch = pending.slice(0, 16);
        const result = await graphql(captureOperations.parts, { input: { mediaId, partNumbers: batch, clientMutationId: `${draft.id}:parts:${batch.join("-")}` } });
        throwOnUserErrors(result.presignMediaParts);
        for (const part of result.presignMediaParts.parts) {
          if (Date.parse(part.expiresAt) <= Date.now()) throw new Error("O acesso temporário expirou. Retome o envio.");
          const body = await readPart(draft, part.partNumber, partSize);
          const response = await fetch(part.url, { method: "PUT", body, headers: { "Content-Type": draft.contentType } });
          const etag = response.headers.get("etag");
          if (!response.ok || !etag) throw new Error("Uma parte do vídeo não foi recebida. Retome o envio.");
          draft = { ...draft, parts: [...draft.parts.filter((item) => item.number !== part.partNumber), { number: part.partNumber, etag }] };
          await saveVideoDraft(draft);
          onProgress?.(draft.parts.length, total);
        }
        pending = pending.slice(batch.length);
      }
      const parts = draft.parts.sort((a, b) => a.number - b.number).map((part) => ({ partNumber: part.number, etag: part.etag ?? "" }));
      if (parts.length !== total || parts.some((part) => !part.etag)) throw new Error("O envio ainda tem partes pendentes.");
      try {
        const completed = await graphql(captureOperations.completeUpload, { input: { mediaId, parts, clientMutationId: `${draft.id}:complete` } });
        throwOnUserErrors(completed.completeMediaUpload);
      } catch (error) {
        const reconciled = await status(mediaId);
        if (!receivedStatuses.has(reconciled.status)) throw error;
      }
    }
    if (!draft.metadataSaved) {
      const saved = await graphql(captureOperations.metadata, { input: { mediaId, requirementKey: draft.requirementKey ?? (draft.kind === "RECAPTURE" ? "video:supplement" : "video:main"), description: draft.requirementKey ? "Clipe do ambiente" : "Vídeo contínuo", captureSource: draft.source, deviceContext: null, gps: null, replacesMediaId: draft.replacesMediaId ?? null, clientMutationId: `${draft.id}:metadata` } });
      throwOnUserErrors(saved.saveCaptureMetadata);
      draft = { ...draft, metadataSaved: true };
    }
    draft = { ...draft, state: "RECEIVED", preparationStatus: "PENDING_VALIDATION" };
    await saveVideoDraft(draft);
    return draft;
  });
}

export async function refreshVideoReceipt(draft: VideoDraft): Promise<VideoDraft> {
  if (!draft.mediaId) return draft;
  const remote = await status(draft.mediaId);
  if (!receivedStatuses.has(remote.status)) return draft;
  let preparationStatus = remote.status;
  // A failed preparation (for example CHECKSUM_MISMATCH) leaves the receipt
  // RECEIVED, so the preparation is always consulted; a RECEIVED receipt only
  // adopts a failure, never an earlier readiness claim.
  try {
    const preparation = (await graphql(captureOperations.videoPreparation, { mediaId: draft.mediaId })).videoPreparation;
    if (remote.status !== "RECEIVED" || preparation.errorCode) preparationStatus = preparation.errorCode || preparation.stage;
  } catch { /* Receipt remains authoritative while preparation status is unavailable. */ }
  const updated = { ...draft, state: "RECEIVED" as const, preparationStatus };
  await saveVideoDraft(updated);
  return updated;
}
