import { expect, test } from "@playwright/test";
import { loginAsLocalAdmin } from "./support/auth";
import {
  authenticateCapture,
  clearPendingDashboardDraft,
  createPhoto,
  createVoiceNote,
  installCatalogItemsMapping,
  installCatalogMapping,
  installTranscriptionTextMapping,
  openCaptureDraft,
  readVoiceNote,
  seedCaptureURL,
  submitCapture,
  validateCatalogMapping,
  wiremockAdminDelete,
} from "./support/voice-stack";

async function discardReviewedDraft(page: import("@playwright/test").Page) {
  await page.getByRole("button", { name: "Descartar rascunho" }).click();
  const dialog = page.getByRole("dialog", { name: "Descartar rascunho" });
  await dialog.getByRole("button", { name: "Descartar rascunho", exact: true }).click();
  await expect(dialog).toHaveCount(0);
}

test("E2E-014: mantém separados cinco pratos e mais dois sem somar quantidades", async ({ page, request }) => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requer a stack local");
  test.setTimeout(240_000);
  let catalog: Awaited<ReturnType<typeof installCatalogItemsMapping>> | undefined;
  const transcriptMaps: Awaited<ReturnType<typeof installTranscriptionTextMapping>>[] = [];
  try {
    transcriptMaps.push(await installTranscriptionTextMapping("cinco pratos fundos brancos na cozinha"));
    const seeded = seedCaptureURL();
    await clearPendingDashboardDraft(page, seeded.inspectionId);
    const session = await authenticateCapture(request, seeded.url, seeded.inspectionId);
    const photo = await createPhoto(session);
    const first = await createVoiceNote(session, { requirementKey: photo.requirementKey, photoMediaId: photo.mediaId });
    await wiremockAdminDelete(transcriptMaps[0]);
    transcriptMaps.push(await installTranscriptionTextMapping("mais dois pratos no armário"));
    const second = await createVoiceNote(session, { requirementKey: photo.requirementKey, photoMediaId: photo.mediaId });
    catalog = await installCatalogItemsMapping([first, second], [
      { name: "Pratos fundos", quantity: 5, noteIndexes: [0] },
      { name: "Pratos no armário", quantity: 2, noteIndexes: [1] },
    ]);
    await validateCatalogMapping(catalog, [first, second]);
    await submitCapture(session);
    await loginAsLocalAdmin(page, `/inspections?inspectionId=${encodeURIComponent(seeded.inspectionId)}`);
    await openCaptureDraft(page, seeded.inspectionId);
    const items = page.getByRole("list", { name: "Itens de Cozinha" });
    await expect(items).toContainText("Pratos fundos");
    await expect(items).toContainText("Pratos no armário");
    const selected = page.getByRole("region", { name: "Item selecionado" });
    await page.getByRole("button", { name: /^Pratos fundos (?:Pendente|Editado)$/ }).click();
    await expect(selected).toContainText("Quantidade 5");
    await page.getByRole("button", { name: "Pratos no armário Pendente" }).click();
    await expect(selected).toContainText("Quantidade 2");
    await expect(page.getByText("Quantidade 7", { exact: true })).toHaveCount(0);
    await discardReviewedDraft(page);
  } finally {
    await wiremockAdminDelete(catalog);
    for (const mapping of transcriptMaps) await wiremockAdminDelete(mapping);
  }
});

test("E2E-015: corrige e aprova uma sugestão de voz, rejeita outra e ativa só a aprovada", async ({ page, request }) => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requer a stack local");
  test.setTimeout(240_000);
  let catalog: Awaited<ReturnType<typeof installCatalogItemsMapping>> | undefined;
  const transcriptMaps: Awaited<ReturnType<typeof installTranscriptionTextMapping>>[] = [];
  try {
    const seeded = seedCaptureURL();
    await clearPendingDashboardDraft(page, seeded.inspectionId);
    const session = await authenticateCapture(request, seeded.url, seeded.inspectionId);
    const photo = await createPhoto(session);
    transcriptMaps.push(await installTranscriptionTextMapping("cinco pratos fundos brancos na cozinha"));
    const one = await createVoiceNote(session, { requirementKey: photo.requirementKey, photoMediaId: photo.mediaId });
    await wiremockAdminDelete(transcriptMaps[0]);
    transcriptMaps.push(await installTranscriptionTextMapping("mais dois pratos no armário"));
    const two = await createVoiceNote(session, { requirementKey: photo.requirementKey, photoMediaId: photo.mediaId });
    catalog = await installCatalogItemsMapping([one, two], [
      { name: "Pratos fundos", quantity: 5, noteIndexes: [0] },
      { name: "Pratos do armário", quantity: 2, noteIndexes: [1] },
    ]);
    await validateCatalogMapping(catalog, [one, two]);
    await submitCapture(session);
    await loginAsLocalAdmin(page, `/inspections?inspectionId=${encodeURIComponent(seeded.inspectionId)}`);
    await openCaptureDraft(page, seeded.inspectionId);
    await page.getByRole("button", { name: "Pratos fundos Pendente" }).click();
    await page.getByRole("button", { name: "Editar Pratos fundos", exact: true }).click();
    const edit = page.getByRole("dialog", { name: "Editar item" });
    await edit.getByLabel("Quantidade", { exact: true }).fill("6");
    await edit.getByRole("button", { name: "Salvar alterações", exact: true }).click();
    await expect(page.getByRole("region", { name: "Item selecionado" })).toContainText("Quantidade 6");
    await page.getByRole("button", { name: "Aceitar Pratos fundos", exact: true }).click();
    await expect(page.getByText("1 incluídos · 0 rejeitados · 1 não revisados", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: /Pratos do armário Pendente/ }).click();
    await page.getByRole("button", { name: "Rejeitar Pratos do armário", exact: true }).click();
    const reject = page.getByRole("dialog", { name: "Rejeitar item" });
    await reject.getByLabel("Motivo da rejeição", { exact: true }).selectOption("DUPLICATE");
    await reject.getByRole("button", { name: "Confirmar rejeição", exact: true }).click();
    await page.getByLabel("Propósito da versão").selectOption("MOVE_IN");
    await page.getByRole("button", { name: "Ativar inventário", exact: true }).click();
    await page.getByRole("dialog", { name: "Ativar inventário" }).getByRole("button", { name: "Ativar inventário", exact: true }).click();
    await expect(page.getByText(/Versão \d+ ativada/)).toBeVisible();
    await expect(page.getByText("1 incluídos · 1 rejeitados · 0 não revisados", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: /Pratos fundos Editado/ }).click();
    await expect(page.getByRole("heading", { name: "Pratos fundos" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Pratos do armário" })).toHaveCount(0);
  } finally {
    await wiremockAdminDelete(catalog);
    for (const mapping of transcriptMaps) await wiremockAdminDelete(mapping);
  }
});

test("E2E-016: promoção mantém itens de voz pendentes fora do inventário ativo", async ({ page, request }) => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requer a stack local");
  test.setTimeout(240_000);
  let catalog: Awaited<ReturnType<typeof installCatalogMapping>> | undefined;
  let transcription: Awaited<ReturnType<typeof installTranscriptionTextMapping>> | undefined;
  try {
    transcription = await installTranscriptionTextMapping("cinco pratos fundos brancos na cozinha");
    const seeded = seedCaptureURL();
    await clearPendingDashboardDraft(page, seeded.inspectionId);
    const session = await authenticateCapture(request, seeded.url, seeded.inspectionId);
    const photo = await createPhoto(session);
    const note = await createVoiceNote(session, { requirementKey: photo.requirementKey, photoMediaId: photo.mediaId });
    catalog = await installCatalogMapping(note);
    await validateCatalogMapping(catalog, note);
    await submitCapture(session);
    await loginAsLocalAdmin(page, `/inspections?inspectionId=${encodeURIComponent(seeded.inspectionId)}`);
    await openCaptureDraft(page, seeded.inspectionId);
    const summary = page.getByRole("region", { name: "Resumo das anotações de voz" });
    await expect(summary).toContainText("Itens de voz pendentes");
    await expect(summary).toContainText("1");
    await expect(page.getByRole("region", { name: "Item selecionado" })).toContainText("Pendente");
    await expect(page.getByRole("button", { name: /Aceitar Pratos/ })).toBeVisible();
    await expect(page.getByText(/0 incluídos/)).toBeVisible();
    await discardReviewedDraft(page);
  } finally {
    await wiremockAdminDelete(catalog);
    await wiremockAdminDelete(transcription);
  }
});

test("E2E-017: regeneração inclui transcrição tardia e conserva sugestão já decidida", async ({ page, request }) => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requer a stack local");
  test.setTimeout(300_000);
  let catalog: Awaited<ReturnType<typeof installCatalogItemsMapping>> | undefined;
  const transcriptMaps: Awaited<ReturnType<typeof installTranscriptionTextMapping>>[] = [];
  try {
    transcriptMaps.push(await installTranscriptionTextMapping("cinco pratos fundos brancos na cozinha"));
    const seeded = seedCaptureURL();
    await clearPendingDashboardDraft(page, seeded.inspectionId);
    const session = await authenticateCapture(request, seeded.url, seeded.inspectionId);
    const photo = await createPhoto(session);
    const decidedNote = await createVoiceNote(session, { requirementKey: photo.requirementKey, photoMediaId: photo.mediaId });
    await wiremockAdminDelete(transcriptMaps[0]);
    transcriptMaps[0] = await installTranscriptionTextMapping("mais dois pratos no armário", 35_000);
    const latePending = await createVoiceNote(session, { requirementKey: photo.requirementKey, photoMediaId: photo.mediaId }, undefined, { waitForTranscription: false });
    catalog = await installCatalogItemsMapping([decidedNote], [{ name: "Pratos fundos", quantity: 5, noteIndexes: [0] }]);
    await validateCatalogMapping(catalog, decidedNote);
    await submitCapture(session);
    await loginAsLocalAdmin(page, `/inspections?inspectionId=${encodeURIComponent(seeded.inspectionId)}`);
    await openCaptureDraft(page, seeded.inspectionId);
    await page.getByRole("button", { name: "Aceitar Pratos fundos", exact: true }).click();
    await expect(page.getByRole("region", { name: "Item selecionado" })).toContainText("Aceito");
    let lateNote: Awaited<ReturnType<typeof readVoiceNote>> | undefined;
    await expect.poll(async () => {
      lateNote = await readVoiceNote(session, latePending.id);
      return lateNote.currentRevisionId;
    }, { timeout: 90_000, message: "a transcrição tardia ficar pronta" }).not.toBe("");
    const regenerated = await installCatalogItemsMapping([lateNote!], [
      { name: "Pratos no armário", quantity: 2, noteIndexes: [0] },
    ]);
    await wiremockAdminDelete(catalog);
    catalog = regenerated;
    const environmentActions = page.locator("details.baseline-room-actions").filter({ hasText: /Notas sem item em/ });
    await environmentActions.locator("summary").click();
    const selectedLate = page.getByRole("group", { name: /Notas sem item em/ }).getByRole("checkbox").last();
    await expect(selectedLate).toBeVisible();
    await selectedLate.check();
    await page.getByRole("button", { name: "Regenerar sugestões selecionadas" }).click();
    await expect(page.getByRole("button", { name: "Pratos no armário Pendente", exact: true })).toBeVisible({ timeout: 90_000 });
    await expect(page.getByRole("button", { name: /Pratos fundos Aceito/ })).toBeVisible();
    await expect(page.getByRole("region", { name: "Resumo das anotações de voz" })).toContainText("1");
    await discardReviewedDraft(page);
  } finally {
    await wiremockAdminDelete(catalog);
    for (const mapping of transcriptMaps) await wiremockAdminDelete(mapping);
  }
});

test("E2E-018: nota de V1 fica inativa após substituição por V2 e mantém citação histórica", async ({ page, request }) => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requer a stack local");
  test.setTimeout(240_000);
  let transcription: Awaited<ReturnType<typeof installTranscriptionTextMapping>> | undefined;
  try {
    transcription = await installTranscriptionTextMapping("cinco pratos fundos brancos na cozinha");
    const seeded = seedCaptureURL();
    await clearPendingDashboardDraft(page, seeded.inspectionId);
    const session = await authenticateCapture(request, seeded.url, seeded.inspectionId);
    const v1 = await createPhoto(session);
    const note = await createVoiceNote(session, { requirementKey: v1.requirementKey, photoMediaId: v1.mediaId });
    const v2 = await createPhoto(session, v1.mediaId);
    expect(v2.mediaId).not.toBe(v1.mediaId);
    await submitCapture(session);
    const historicalNote = await readVoiceNote(session, note.id);
    expect(historicalNote.photoMediaId).toBe(v1.mediaId);
    expect(historicalNote.currentRevisionId).toBe(note.currentRevisionId);
    await loginAsLocalAdmin(page, `/inspections?inspectionId=${encodeURIComponent(seeded.inspectionId)}`);
    await expect(page.getByRole("heading", { name: "Anotações de voz" })).toBeVisible();
    const review = page.getByRole("region", { name: "Revisão da anotação de voz" });
    await expect(review.getByRole("region", { name: "Texto original" })).toContainText(note.text);
    await expect(review.getByRole("region", { name: "Revisão atual" })).toContainText(note.text);
    await expect(review.getByRole("region", { name: "Âncora da anotação" })).toContainText("Mídia substituída; esta nota continua ligada à mídia original");
    await clearPendingDashboardDraft(page, seeded.inspectionId);
  } finally {
    await wiremockAdminDelete(transcription);
  }
});
