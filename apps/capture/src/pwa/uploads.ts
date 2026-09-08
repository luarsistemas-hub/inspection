import { captureOperations, graphql } from "@/graphql/client";
import { type CaptureDraft, type PendingPart, saveDraft } from "@/pwa/drafts";

const partSize = 5 * 1024 * 1024;
type UserErrors = { userErrors: Array<{ message: string }> };

const failOnError = (value: UserErrors): void => { if (value.userErrors[0]) throw new Error(value.userErrors[0].message); };
const partsFor = (blob: Blob): PendingPart[] => Array.from({ length: Math.ceil(blob.size / partSize) }, (_, index) => ({ number: index + 1, complete: false }));
const wait = (milliseconds: number) => new Promise((resolve) => setTimeout(resolve, milliseconds));

async function withDraftLock<T>(draftId: string, action: () => Promise<T>): Promise<T> {
  if (navigator.locks) return navigator.locks.request(`capture-upload:${draftId}`, { mode: "exclusive" }, action);
  return action();
}

export function uploadDraft(draft: CaptureDraft): Promise<CaptureDraft> {
  return withDraftLock(draft.id, async () => {
    let current = draft;
    if (!current.mediaId || !current.uploadId) {
      const created = await graphql<{ createMediaUpload: { upload?: { mediaId: string; uploadId: string }; userErrors: Array<{ message: string }> } }>(captureOperations.createUpload, { contentType: draft.blob.type, size: draft.blob.size, hash: draft.sha256, id: draft.id });
      failOnError(created.createMediaUpload);
      if (!created.createMediaUpload.upload) throw new Error("A API não retornou o upload de mídia.");
      current = { ...draft, ...created.createMediaUpload.upload, parts: partsFor(draft.blob) };
      await saveDraft(current);
    }
    const pending = current.parts.filter((part) => !part.complete).map((part) => part.number);
    if (pending.length) {
      const signed = await graphql<{ presignMediaParts: { parts: Array<{ partNumber: number; url: string }>; userErrors: Array<{ message: string }> } }>(captureOperations.parts, { mediaId: current.mediaId, parts: pending, id: `${draft.id}:parts` });
      failOnError(signed.presignMediaParts);
      for (const part of signed.presignMediaParts.parts) {
        const response = await fetch(part.url, { method: "PUT", body: current.blob.slice((part.partNumber - 1) * partSize, part.partNumber * partSize), headers: { "Content-Type": current.blob.type } });
        const etag = response.headers.get("etag");
        if (!response.ok || !etag) throw new Error("Não foi possível enviar uma parte da foto.");
        current = { ...current, parts: current.parts.map((saved) => saved.number === part.partNumber ? { ...saved, complete: true, etag } : saved) };
        await saveDraft(current);
      }
    }
    const completed = await graphql<{ completeMediaUpload: UserErrors }>(captureOperations.completeUpload, { mediaId: current.mediaId, parts: current.parts.map((part) => ({ partNumber: part.number, etag: part.etag })), id: `${draft.id}:complete` });
    failOnError(completed.completeMediaUpload);
    let lastError: unknown;
    for (let attempt = 0; attempt < 8; attempt++) {
      try {
        const metadata = await graphql<{ saveCaptureMetadata: UserErrors }>(captureOperations.metadata, { mediaId: current.mediaId, key: current.metadata.requirementKey, description: current.metadata.description, id: `${draft.id}:metadata` });
        failOnError(metadata.saveCaptureMetadata);
        current = { ...current, metadataSaved: true };
        await saveDraft(current);
        return current;
      } catch (error) {
        lastError = error;
        const message = error instanceof Error ? error.message : String(error);
        if (attempt === 7 || !/pending|verification|screening|processing|network|fetch/i.test(message)) break;
        await wait(250 * (attempt + 1));
      }
    }
    throw lastError instanceof Error ? lastError : new Error("A verificação da foto ainda está em processamento. Tente retomar o envio.");
  });
}
