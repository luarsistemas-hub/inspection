import { expect, test } from "@playwright/test";
import {
  authenticateCapture,
  authorizeVoiceFromDisclosure,
  installTransientTranscriptionFailure,
  openAdminVoicePolicy,
  openDashboardInspection,
  probeDashboardVideo,
  recordVideoWithVoice,
  recordVoiceNote,
  removeAllTransientWireMockMappings,
  removeWireMockMapping,
  saveEnvironmentPhoto,
  seedVoiceCapture,
} from "./support/voice-stack";

test.describe("jornadas reais de voz na stack local", () => {
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requer a stack local autenticada");
  test.afterEach(async () => { await removeAllTransientWireMockMappings(); });

  test("E2E-001: salva a política no Admin e mostra os limites efetivos no Capture", async ({ page, request }) => {
    test.setTimeout(120_000);
    const seeded = seedVoiceCapture("PHOTO");
    await openAdminVoicePolicy(page);

    const tenantNotes = page.getByLabel("Máximo de anotações por imobiliária");
    const tenantMinutes = page.getByLabel("Áudio total máximo por imobiliária (minutos)");
    const inspectionNotes = page.getByLabel("Máximo de anotações por vistoria");
    const inspectionMinutes = page.getByLabel("Áudio total máximo por vistoria (minutos)");
    const enabled = page.getByLabel("Habilitar novas anotações por voz");
    const save = page.getByRole("button", { name: "Salvar política de voz" });
    const apply = async (values: [string, string, string, string]) => {
      await tenantNotes.fill(values[0]);
      await tenantMinutes.fill(values[1]);
      await inspectionNotes.fill(values[2]);
      await inspectionMinutes.fill(values[3]);
      if (!(await enabled.isChecked())) await enabled.check();
      await save.click();
      await expect(page.locator(".admin-form p[role=status]")).toContainText("Anotações por voz habilitadas");
    };

    // A temporary save proves the Admin mutation and reload path even when seed already has target values.
    try {
      await apply(["101", "301", "21", "31"]);
    } finally {
      await apply(["100", "300", "20", "30"]);
    }
    await expect(page.locator('aside[aria-label="Limites efetivos"]')).toContainText("100 anotações · 300 min de áudio");
    await expect(page.locator('aside[aria-label="Limites efetivos"]')).toContainText("20 anotações · 30 min de áudio");

    await page.context().clearCookies();
    await authenticateCapture(page, request, seeded.url, "PHOTO");
    const voiceLimit = page.getByText("Limite desta vistoria: até 20 anotações e 30 min de áudio.");
    if (!(await voiceLimit.isVisible().catch(() => false))) await page.getByRole("button", { name: /Sala/ }).first().click();
    await expect(voiceLimit).toBeVisible();
  });

  test("E2E-003: anota intervalo no vídeo, abre a nota no Dashboard e prova que o objeto não tem áudio", async ({ page, request }) => {
    test.skip(test.info().project.name !== "desktop-chrome", "MediaRecorder e ffprobe desta jornada são validados no Chromium local.");
    test.setTimeout(180_000);
    const seeded = seedVoiceCapture("VIDEO");
    const observedNotes: Array<Record<string, unknown>> = [];
    page.on("request", (outgoing) => {
      if (outgoing.url().endsWith("/graphql") && String(outgoing.postDataJSON()?.query ?? "").includes("CreateAudioNoteUpload")) {
        observedNotes.push((outgoing.postDataJSON()?.variables as { input: Record<string, unknown> }).input);
      }
    });
    await authenticateCapture(page, request, seeded.url, "VIDEO");
    await recordVideoWithVoice(page);
    await expect(page.getByText(/Vídeo · \d+–\d+ s \(aproximado\)/)).toBeVisible();
    expect(observedNotes).toHaveLength(1);
    expect(observedNotes[0].requirementKey).toMatch(/^env:/);
    expect(observedNotes[0].timeBasis).toBe("CAPTURE_CLOCK");

    const dashboard = await page.context().newPage();
    let listedNote: { startMs: number | null; endMs: number | null; anchor: { playbackUrl: string | null } } | undefined;
    let detailNote: { anchor: { playbackUrl: string | null } } | undefined;
    dashboard.on("response", async (response) => {
      if (!response.url().endsWith("/graphql")) return;
      try {
        const body = await response.json() as { data?: { audioNotes?: { nodes?: typeof listedNote[] }; audioNote?: typeof detailNote } };
        listedNote = body.data?.audioNotes?.nodes?.[0] as typeof listedNote;
        if (body.data?.audioNote) detailNote = body.data.audioNote;
      } catch { /* other authenticated responses may have no JSON body */ }
    });
    await openDashboardInspection(dashboard, seeded.inspectionId);
    const panel = dashboard.getByRole("region", { name: "Anotações de voz" });
    const intervalButton = panel.getByRole("button", { name: /Nota 1, .*\d+:\d+–\d+:\d+/ });
    await expect(intervalButton).toBeVisible({ timeout: 60_000 });
    await intervalButton.click();
    const review = dashboard.getByRole("region", { name: /Revisão da anotação de voz/ });
    await expect(review.getByRole("region", { name: "Âncora da anotação" }).getByText(/Intervalo aproximado \d+:\d+–\d+:\d+/)).toBeVisible();
    const video = dashboard.getByLabel("Reprodução do vídeo da anotação");
    await expect.poll(async () => {
      if (await video.isVisible().catch(() => false)) return true;
      const refresh = review.getByRole("button", { name: "Atualizar" });
      if (await refresh.isVisible().catch(() => false) && await refresh.isEnabled()) await refresh.click();
      return false;
    }, { timeout: 60_000, intervals: [1_000, 2_000, 3_000], message: "A mídia do intervalo deve ficar disponível no Dashboard." }).toBe(true);
    expect(listedNote?.startMs).not.toBeNull();
    expect(listedNote?.endMs).not.toBeNull();
    await expect.poll(() => detailNote?.anchor.playbackUrl ?? "", { timeout: 15_000, message: "A consulta autorizada da nota deve incluir a URL da mídia." }).not.toBe("");
    const probe = await probeDashboardVideo(dashboard, detailNote!.anchor.playbackUrl!);
    expect(probe.streams.filter((stream) => stream.codec_type === "audio")).toHaveLength(0);
    expect(probe.streams.some((stream) => stream.codec_type === "video")).toBe(true);
  });

  test("E2E-008: mostra nota na fila e pronta; falha terminal oferece fallback e não impede envio visual", async ({ page, request }) => {
    test.skip(test.info().project.name !== "desktop-chrome", "A gravação sintética usa MediaRecorder nativo do Chromium.");
    test.setTimeout(240_000);
    const seeded = seedVoiceCapture("PHOTO");
    await authenticateCapture(page, request, seeded.url, "PHOTO");
    await saveEnvironmentPhoto(page, "Sala", `voice-queue-${Date.now()}`);

    await recordVoiceNote(page);
    const noteRows = page.locator("[data-note-id]");
    await expect(page.getByText("Anotação recebida e aguardando processamento.")).toBeVisible({ timeout: 30_000 });
    await expect(noteRows.first()).toContainText("Na fila de transcrição", { timeout: 30_000 });
    await expect(noteRows.first()).toContainText("Texto pronto para revisão", { timeout: 90_000 });

    let mappingId: string | undefined;
    try {
      mappingId = await installTransientTranscriptionFailure(request);
      await recordVoiceNote(page);
      await expect(page.getByText("Anotação recebida e aguardando processamento.")).toBeVisible({ timeout: 30_000 });
      await expect(noteRows.nth(1)).toContainText("Transcrição não concluída", { timeout: 150_000 });
      await noteRows.nth(1).getByText("Revisar anotação").click();
      const failedReview = noteRows.nth(1).locator("details");
      await expect(failedReview.getByText("Tente novamente ou escreva a descrição abaixo.")).toBeVisible();
      const declaration = failedReview.getByLabel("Escrever a descrição");
      await expect(declaration).toBeVisible();
      await declaration.fill("A torneira apresenta vazamento na conexão.");
      await failedReview.getByRole("button", { name: "Salvar minha descrição" }).click();
      await expect(page.getByRole("region", { name: "Anotações da foto 1" })).toContainText("Descrição salva como declaração sua.");

      await saveEnvironmentPhoto(page, "Cozinha", `voice-queue-kitchen-${Date.now()}`);
      await page.getByRole("button", { name: "Revisar vistoria" }).click();
      await expect(page.getByRole("heading", { name: "Revise antes de enviar" })).toBeVisible();
      await page.getByRole("button", { name: "Enviar vistoria completa" }).click();
      await expect(page.getByRole("heading", { name: "Recebemos sua confirmação" })).toBeVisible();
    } finally {
      if (mappingId) await removeWireMockMapping(mappingId);
    }
  });

  test("E2E-009: operador remove o destaque sem alterar a revisão nem a decisão de inventário", async ({ page, request }) => {
    test.skip(test.info().project.name !== "desktop-chrome", "A gravação sintética usa MediaRecorder nativo do Chromium.");
    test.setTimeout(180_000);
    const seeded = seedVoiceCapture("PHOTO");
    await authenticateCapture(page, request, seeded.url, "PHOTO");
    await saveEnvironmentPhoto(page, "Sala", `voice-highlight-${Date.now()}`);
    await recordVoiceNote(page);
    const noteRow = page.locator("[data-note-id]").first();
    await expect(noteRow).toContainText("Texto pronto para revisão", { timeout: 90_000 });
    await noteRow.getByText("Revisar anotação").click();
    await noteRow.getByRole("button", { name: "Destacar anotação" }).click();
    await expect(noteRow).toContainText("Destacada");

    const dashboard = await page.context().newPage();
    await openDashboardInspection(dashboard, seeded.inspectionId);
    const review = dashboard.getByRole("region", { name: /Revisão da anotação de voz/ });
    await expect(review.getByText("Destaque do participante")).toBeVisible({ timeout: 60_000 });
    await expect(review.getByText("A confirmação abaixo se aplica somente ao texto; não decide nenhum item do inventário.")).toBeVisible();
    const originalText = await review.getByRole("region", { name: "Texto original" }).innerText();
    const currentText = await review.getByRole("region", { name: "Revisão atual" }).innerText();
    await review.getByRole("button", { name: "Remover destaque" }).click();
    const confirmation = dashboard.getByRole("alertdialog", { name: "Remover destaque da anotação?" });
    await expect(confirmation).toContainText("Esta ação remove apenas o destaque e não altera nenhum item do inventário.");
    await confirmation.getByRole("button", { name: "Remover destaque" }).click();
    await expect(review.getByText("Destaque do participante")).toHaveCount(0);
    await expect.poll(() => review.getByRole("region", { name: "Texto original" }).innerText()).toBe(originalText);
    await expect.poll(() => review.getByRole("region", { name: "Revisão atual" }).innerText()).toBe(currentText);
    await expect(dashboard.getByRole("alertdialog")).toHaveCount(0);
  });

  test("E2E-021: lê o aviso, mantém a transcrição e mostra áudio indisponível após a exclusão", async ({ page, request }) => {
    test.skip(test.info().project.name !== "desktop-chrome", "A gravação sintética usa MediaRecorder nativo do Chromium.");
    test.setTimeout(180_000);
    const seeded = seedVoiceCapture("PHOTO");
    const observedNoteStates: Array<{ status: string; audioAvailability: string; audioDeletionStatus: string; revisions: Array<{ text: string }> }> = [];
    page.on("response", async (response) => {
      if (!response.url().endsWith("/graphql")) return;
      try {
        const body = await response.json() as { data?: { audioNote?: (typeof observedNoteStates)[number] | null } };
        if (body.data?.audioNote) observedNoteStates.push(body.data.audioNote);
      } catch { /* ignore other GraphQL payloads */ }
    });
    await authenticateCapture(page, request, seeded.url, "PHOTO", false);
    await saveEnvironmentPhoto(page, "Sala", `voice-retention-${Date.now()}`, false);
    await authorizeVoiceFromDisclosure(page, "foto");
    await recordVoiceNote(page);
    const noteRow = page.locator("[data-note-id]").first();
    await expect(noteRow).toContainText("Texto pronto para revisão", { timeout: 90_000 });
    await noteRow.getByText("Revisar anotação").click();
    await expect(noteRow).toContainText("Áudio original indisponível; o texto pode ser revisado.", { timeout: 90_000 });
    await expect.poll(() => observedNoteStates.some((note) => note.status === "COMPLETED" && note.audioAvailability !== "AVAILABLE" && note.audioDeletionStatus === "DELETED"), {
      timeout: 90_000,
      message: "A captura deve observar a exclusão concluída do áudio original após a transcrição.",
    }).toBe(true);
    await expect(noteRow).toContainText("Texto atual");
    await expect(noteRow).toContainText("cinco pratos fundos brancos na cozinha");
  });
});
