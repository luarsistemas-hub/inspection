import { graphql } from "@/graphql/client";
import { ConfirmAudioNoteTextDocument, DiscardAudioNoteDocument, RetryAudioNoteDocument, ReviseAudioNoteDocument, SetAudioNoteHighlightDocument } from "@/graphql/generated";
import { throwOnUserErrors } from "@/pwa/mutation-errors";
import { deleteAudioDraft } from "./audio-drafts";
import { VoiceFlowError, maximumNoteTextLength, textDigest, type NoteItem } from "./audio-notes-state";

function requireNote(item: NoteItem) {
  if (!item.note) throw new VoiceFlowError("NOTE_UNAVAILABLE", "O status desta anotação ainda não está disponível. Aguarde a conexão e tente novamente.");
  return item.note;
}

/** Validates a correction before any request so an empty or oversized text never replaces the last revision. */
export function validateNoteText(text: string): string | undefined {
  if (!text.trim()) return "Digite o texto da anotação antes de salvar.";
  if (text.length > maximumNoteTextLength) return `O texto pode ter até ${maximumNoteTextLength.toLocaleString("pt-BR")} caracteres.`;
  return undefined;
}

/** Saves a human correction as a new revision; it never requests another paid transcription. */
export async function reviseNoteText(item: NoteItem, text: string, kind: "HUMAN_EDIT" | "PARTICIPANT_DECLARATION"): Promise<void> {
  const invalid = validateNoteText(text);
  if (invalid) throw new VoiceFlowError("INVALID_INPUT", invalid);
  const note = requireNote(item);
  const clean = text.trim();
  const result = await graphql(ReviseAudioNoteDocument, { input: { noteId: note.id, text: clean, revisionKind: kind, expectedVersion: note.version, clientMutationId: `${item.draft.id}:revise:${note.version}:${textDigest(clean)}` } });
  throwOnUserErrors(result.reviseAudioNote);
}

/** Confirms the current text without deciding any inventory item. */
export async function confirmNoteText(item: NoteItem): Promise<void> {
  const note = requireNote(item);
  const result = await graphql(ConfirmAudioNoteTextDocument, { input: { noteId: note.id, expectedVersion: note.version, clientMutationId: `${item.draft.id}:confirm:${note.version}` } });
  throwOnUserErrors(result.confirmAudioNoteText);
}

/** Sets an explicit highlight; the same request twice keeps one mark. */
export async function setNoteHighlight(item: NoteItem, highlighted: boolean): Promise<void> {
  const note = requireNote(item);
  const result = await graphql(SetAudioNoteHighlightDocument, { input: { noteId: note.id, highlighted, expectedVersion: note.version, clientMutationId: `${item.draft.id}:highlight:${note.version}:${highlighted ? "on" : "off"}` } });
  throwOnUserErrors(result.setAudioNoteHighlight);
}

/** Retries the original transcription while its temporary audio still exists. */
export async function retryNote(item: NoteItem): Promise<void> {
  const note = requireNote(item);
  const result = await graphql(RetryAudioNoteDocument, { input: { noteId: note.id, expectedVersion: note.version, clientMutationId: `${item.draft.id}:retry:${note.version}` } });
  throwOnUserErrors(result.retryAudioNote);
}

/** Discards a note: the server first when it already knows the note, then the local record and its bytes. */
export async function discardNote(item: NoteItem): Promise<void> {
  if (item.draft.noteId && item.note) {
    const result = await graphql(DiscardAudioNoteDocument, { input: { noteId: item.note.id, expectedVersion: item.note.version, clientMutationId: `${item.draft.id}:discard:${item.note.version}` } });
    throwOnUserErrors(result.discardAudioNote);
    return;
  }
  if (item.draft.noteId) throw new VoiceFlowError("NOTE_UNAVAILABLE", "Esta anotação já foi enviada. Aguarde a conexão para descartá-la.");
  await deleteAudioDraft(item.draft.id, item.draft.responsibilityId);
}
