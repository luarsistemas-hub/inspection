import { expect, request as playwrightRequest, test } from "@playwright/test";
import { loginAsLocalAdmin } from "./support/auth";
import {
  authenticateCapture,
  advanceVideoSourceExpiryClock,
  clearPendingDashboardDraft,
  createPhoto,
  createVideo,
  createVoiceNote,
  expectVoiceNoteIneligible,
  installCatalogMapping,
  installTranscriptionTextMapping,
  openCaptureDraft,
  promoteFromDashboard,
  promotionStatus,
  readVoiceNote,
  seedCaptureURL,
  submitCapture,
  validateCatalogMapping,
  waitForVideoRetention,
  wiremockAdminDelete,
} from "./support/voice-stack";

test("E2E-010: corrige e confirma N1 sem decidir o item de voz", async ({ page, request }) => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requer a stack local");
  test.setTimeout(180_000);
  let transcriptionMapping: Awaited<ReturnType<typeof installTranscriptionTextMapping>> | undefined;
  let catalogMapping: Awaited<ReturnType<typeof installCatalogMapping>> | undefined;
  try {
    transcriptionMapping = await installTranscriptionTextMapping("seis pratos fundos brancos na cozinha");
    const seeded = seedCaptureURL("PHOTO");
    await clearPendingDashboardDraft(page, seeded.inspectionId);
    const session = await authenticateCapture(request, seeded.url, seeded.inspectionId);
    const photo = await createPhoto(session);
    const note = await createVoiceNote(session, { requirementKey: photo.requirementKey, photoMediaId: photo.mediaId });
    expect(note.text).toContain("seis pratos");
    catalogMapping = await installCatalogMapping(note);
    await validateCatalogMapping(catalogMapping, note);
    await submitCapture(session);

    await loginAsLocalAdmin(page, `/inspections?inspectionId=${encodeURIComponent(seeded.inspectionId)}`);
    await expect(page.getByRole("heading", { name: "Anotações de voz" })).toBeVisible();
    const review = page.getByRole("region", { name: "Revisão da anotação de voz" });
    await expect(review.getByRole("region", { name: "Texto original" })).toContainText("seis pratos");
    await review.getByLabel("Corrigir texto da anotação").fill("cinco pratos fundos brancos na cozinha");
    await review.getByRole("button", { name: "Salvar correção" }).click();
    await expect(review.getByRole("region", { name: "Revisão atual" })).toContainText("cinco pratos fundos brancos na cozinha");
    await review.getByRole("button", { name: "Confirmar texto" }).click();
    await expect(review.getByText("Texto confirmado · somente leitura", { exact: true })).toBeVisible();

    const revised = await readVoiceNote(session, note.id);
    expect(revised.text).toBe("cinco pratos fundos brancos na cozinha");
    await openCaptureDraft(page, seeded.inspectionId);
    const summary = page.getByRole("region", { name: "Resumo das anotações de voz" });
    await expect(summary).toContainText("Sugestões baseadas em texto antigo");
    await expect(summary).toContainText("1");
    const item = page.getByRole("region", { name: "Item selecionado" });
    await expect(item).toContainText("Pendente");
    await expect(page.getByRole("button", { name: /Aceitar Pratos/ })).toBeVisible();
    await clearPendingDashboardDraft(page, seeded.inspectionId);
  } finally {
    await wiremockAdminDelete(catalogMapping);
    await wiremockAdminDelete(transcriptionMapping);
  }
});

test("E2E-011: abre a foto e o intervalo da nota e preserva o quadro após a retenção", async ({ page, context, request }) => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requer a stack local");
  test.setTimeout(240_000);
  const photoSeed = seedCaptureURL("PHOTO");
  await clearPendingDashboardDraft(page, photoSeed.inspectionId);
  const photoRequest = await playwrightRequest.newContext();
  const photoSession = await authenticateCapture(photoRequest, photoSeed.url, photoSeed.inspectionId);
  const photo = await createPhoto(photoSession);
  const photoNote = await createVoiceNote(photoSession, { requirementKey: photo.requirementKey, photoMediaId: photo.mediaId });
  await submitCapture(photoSession);

  const videoSeed = seedCaptureURL("VIDEO");
  const videoRequest = await playwrightRequest.newContext();
  const videoSession = await authenticateCapture(videoRequest, videoSeed.url, videoSeed.inspectionId);
  const video = await createVideo(videoSession);
  const videoNote = await createVoiceNote(videoSession, { requirementKey: video.requirementKey, videoAttemptId: video.attemptId, startMs: 800, endMs: 1600 });
  await submitCapture(videoSession);

  try {
    await loginAsLocalAdmin(page, `/inspections?inspectionId=${encodeURIComponent(photoSeed.inspectionId)}`);
    await expect(page.getByRole("heading", { name: "Anotações de voz" })).toBeVisible();
    const photoAnchor = page.getByRole("region", { name: "Âncora da anotação" });
    await expect(photoAnchor.getByRole("heading", { name: "Foto associada" })).toBeVisible();
    await expect(photoAnchor.locator("img[alt^='Foto do cômodo']")).toBeVisible();

    const videoPage = await context.newPage();
    try {
      await loginAsLocalAdmin(videoPage, `/inspections?inspectionId=${encodeURIComponent(videoSeed.inspectionId)}`);
      await expect(videoPage.getByRole("heading", { name: "Anotações de voz" })).toBeVisible();
      const videoAnchor = videoPage.getByRole("region", { name: "Âncora da anotação" });
      await expect(videoAnchor.getByText("Intervalo aproximado 00:00–00:01", { exact: true })).toBeVisible();
      await expect(videoAnchor.getByRole("heading", { name: "Contexto visual" })).toBeVisible();
      expect(videoNote.sourceMediaId).toBeTruthy();
      advanceVideoSourceExpiryClock(video.mediaId);
      await waitForVideoRetention(videoSession, videoNote.id, video.mediaId);
      await videoPage.getByRole("button", { name: "Atualizar" }).click();
      await expect(videoAnchor.getByText("Vídeo original indisponível", { exact: true })).toBeVisible({ timeout: 30_000 });
      await expect(videoAnchor.getByRole("heading", { name: "Contexto visual" })).toBeVisible();
      await expect(videoAnchor.getByText(/\d{2}:\d{2} · Disponível/)).toBeVisible();
      await expect(videoPage.locator("a[href*='X-Amz'], img[src*='X-Amz'], video[src*='X-Amz']")).toHaveCount(0);
    } finally {
      await videoPage.close();
    }
  } finally {
    await photoRequest.dispose();
    await videoRequest.dispose();
  }
  await clearPendingDashboardDraft(page, videoSeed.inspectionId);
});

test("E2E-012: cataloga cinco pratos por voz e mantém o fluxo visual", async ({ page, context, request }) => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requer a stack local");
  test.setTimeout(240_000);
  let voiceCatalog: Awaited<ReturnType<typeof installCatalogMapping>> | undefined;
  try {
    const voiceSeed = seedCaptureURL("PHOTO");
    await clearPendingDashboardDraft(page, voiceSeed.inspectionId);
    const voiceSession = await authenticateCapture(request, voiceSeed.url, voiceSeed.inspectionId);
    const voicePhoto = await createPhoto(voiceSession);
    const voiceNote = await createVoiceNote(voiceSession, { requirementKey: voicePhoto.requirementKey, photoMediaId: voicePhoto.mediaId });
    expect(voiceNote.text).toContain("cinco pratos");
    voiceCatalog = await installCatalogMapping(voiceNote);
    await validateCatalogMapping(voiceCatalog, voiceNote);
    await submitCapture(voiceSession);

    await loginAsLocalAdmin(page, `/inspections?inspectionId=${encodeURIComponent(voiceSeed.inspectionId)}`);
    await openCaptureDraft(page, voiceSeed.inspectionId);
    const voiceSummary = page.getByRole("region", { name: "Resumo das anotações de voz" });
    await expect(voiceSummary).toContainText("Itens de voz pendentes");
    await expect(page.getByRole("region", { name: "Item selecionado" })).toContainText("Pratos");
    await expect(page.getByRole("region", { name: "Item selecionado" })).toContainText("Quantidade 5");
    await expect(page.getByText("Informado por voz · quantity", { exact: false })).toBeVisible();
    await page.getByRole("button", { name: "Descartar rascunho" }).click();
    const discardDialog = page.getByRole("dialog", { name: "Descartar rascunho" });
    await discardDialog.getByRole("button", { name: "Descartar rascunho", exact: true }).click();

    const visualSeed = seedCaptureURL("PHOTO");
    const visualRequest = await playwrightRequest.newContext();
    try {
      const visualSession = await authenticateCapture(visualRequest, visualSeed.url, visualSeed.inspectionId);
      await createPhoto(visualSession);
      await submitCapture(visualSession);
      const visualPage = await context.newPage();
      try {
        await loginAsLocalAdmin(visualPage, `/inspections?inspectionId=${encodeURIComponent(visualSeed.inspectionId)}`);
        await promoteFromDashboard(visualPage);
        await expect(promotionStatus(visualPage)).toContainText(/Versão \d+ criada e ativa\./, { timeout: 90_000 });
      } finally { await visualPage.close(); }
    } finally { await visualRequest.dispose(); }
  } finally {
    await wiremockAdminDelete(voiceCatalog);
  }
});

test("E2E-013: exige revisão explícita da quantidade falada quando a foto só mostra parte do item", async ({ page, request }) => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requer a stack local");
  test.setTimeout(180_000);
  let catalogMapping: Awaited<ReturnType<typeof installCatalogMapping>> | undefined;
  try {
    const seeded = seedCaptureURL("PHOTO");
    await clearPendingDashboardDraft(page, seeded.inspectionId);
    const session = await authenticateCapture(request, seeded.url, seeded.inspectionId);
    const photo = await createPhoto(session);
    const note = await createVoiceNote(session, { requirementKey: photo.requirementKey, photoMediaId: photo.mediaId });
    catalogMapping = await installCatalogMapping(note, { visualMediaId: photo.mediaId, visibleIdentity: true, quantityConflict: true });
    await validateCatalogMapping(catalogMapping, note);
    await submitCapture(session);

    await loginAsLocalAdmin(page, `/inspections?inspectionId=${encodeURIComponent(seeded.inspectionId)}`);
    await openCaptureDraft(page, seeded.inspectionId);
    const item = page.getByRole("region", { name: "Item selecionado" });
    await expect(item).toContainText("Pratos");
    await expect(item).toContainText("Quantidade 5");
    await expect(item.getByText("Revisão necessária · quantity", { exact: true })).toBeVisible();
    await expect(item.getByText("Imagem: 4", { exact: true })).toBeVisible();
    await expect(item.getByText("Voz: 5", { exact: true })).toBeVisible();
    await item.getByRole("button", { name: "Aceitar Pratos" }).click();
    await expect(item).toContainText("Aceito");
    await page.getByRole("button", { name: "Descartar rascunho" }).click();
    const discardDialog = page.getByRole("dialog", { name: "Descartar rascunho" });
    await discardDialog.getByRole("button", { name: "Descartar rascunho", exact: true }).click();
  } finally {
    await wiremockAdminDelete(catalogMapping);
  }
});

test("E2E-023: vídeo contínuo e vistoria visual seguem o catálogo sem fontes de voz", async ({ page, context, request }) => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requer a stack local");
  test.setTimeout(240_000);
  const videoSeed = seedCaptureURL("VIDEO", false);
  await clearPendingDashboardDraft(page, videoSeed.inspectionId);
  const videoRequest = await playwrightRequest.newContext();
  const videoSession = await authenticateCapture(videoRequest, videoSeed.url, videoSeed.inspectionId, { allowVoiceDisabled: true });
  try {
    const video = await createVideo(videoSession);
    const ineligible = await expectVoiceNoteIneligible(videoSession, { requirementKey: video.requirementKey, videoAttemptId: video.attemptId });
    expect(ineligible).toEqual({ code: "INVALID_STATE", message: "Esta vistoria não é elegível para anotações de voz." });
    await submitCapture(videoSession);
    await loginAsLocalAdmin(page, `/inspections?inspectionId=${encodeURIComponent(videoSeed.inspectionId)}`);
    await expect(page.getByRole("heading", { name: "Anotações de voz" })).toHaveCount(0);
    const promotion = page.getByRole("region", { name: "Promoção da vistoria para origem" });
    await expect(promotion).toContainText("A captura submetida não possui ambientes com requisitos de evidência.");
    await expect(promotion.getByRole("button", { name: "Usar esta vistoria como origem", exact: true })).toHaveCount(0);
    expect(await catalogJournalBody(video.mediaId)).toBe("");

    const visualSeed = seedCaptureURL("PHOTO");
    const visualRequest = await playwrightRequest.newContext();
    try {
      const visualSession = await authenticateCapture(visualRequest, visualSeed.url, visualSeed.inspectionId);
      const visualPhoto = await createPhoto(visualSession);
      await submitCapture(visualSession);
      const visualPage = await context.newPage();
      try {
        await loginAsLocalAdmin(visualPage, `/inspections?inspectionId=${encodeURIComponent(visualSeed.inspectionId)}`);
        await expect(visualPage.getByRole("heading", { name: "Anotações de voz" })).toHaveCount(0);
        await promoteFromDashboard(visualPage);
        await expect(promotionStatus(visualPage)).toContainText(/Versão \d+ criada e ativa\./, { timeout: 90_000 });
        await visualPage.getByRole("button", { name: "Ver origem" }).click();
        await expect(visualPage.getByRole("heading", { name: "Revisar inventário" })).toBeVisible();
        await expect(visualPage.getByRole("region", { name: "Resumo das anotações de voz" })).toHaveCount(0);
        const visualCatalogRequest = await catalogJournalBody(visualPhoto.mediaId);
        expect(visualCatalogRequest).toContain("catalog one captured environment; include fixed property components and exclude personal or everyday objects");
        expect(visualCatalogRequest).not.toContain("voiceNotes");
        expect(visualCatalogRequest).not.toContain("catalog every environment visible in the legacy origin evidence");
      } finally { await visualPage.close(); }
    } finally { await visualRequest.dispose(); }
  } finally {
    await videoRequest.dispose();
  }
});

async function catalogJournalBody(evidenceId: string): Promise<string> {
  const wiremock = process.env.INSPECTION_E2E_WIREMOCK_URL ?? "http://localhost:18080";
  const response = await fetch(`${wiremock}/__admin/requests`);
  if (!response.ok) throw new Error(`Não foi possível ler o journal do WireMock (${response.status}).`);
  const journal = await response.json() as { requests?: Array<{ request?: { url?: string; body?: unknown; bodyAsBase64?: string } }> };
  const entry = journal.requests?.find(({ request: item }) => {
    if (!item?.url?.includes("/v1/chat/completions")) return false;
    const body = typeof item.body === "string" ? item.body : JSON.stringify(item.body ?? "");
    const decoded = item.bodyAsBase64 ? Buffer.from(item.bodyAsBase64, "base64").toString("utf8") : "";
    // Visual analysis requests cite the same media; only the inventory catalog call matters here.
    return [body, decoded].some((text) => text.includes(evidenceId) && text.includes("INVENTORY_CATALOG"));
  });
  if (!entry?.request) return "";
  let body = typeof entry.request.body === "string" ? entry.request.body : JSON.stringify(entry.request.body ?? "");
  if (!body && entry.request.bodyAsBase64) body = Buffer.from(entry.request.bodyAsBase64, "base64").toString("utf8");
  return body;
}

