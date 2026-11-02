import { expect, test, type Page } from "@playwright/test";
import { installRuntimeGuards, navigateAdmin } from "./support/auth";
import {
  authenticateVoiceCapture,
  configureVoiceAdmission,
  denyAudioDeletionForNote,
  getAudioNote,
  installMissingCostTranscriptionMapping,
  installTransientTranscriptionMapping,
  loginVoiceOperationsAdmin,
  prepareRealVoiceRecorder,
  recordRealVoiceNote,
  removeWiremockMapping,
  restoreVoiceAdmission,
  seedVoiceCaptureURL,
  type AudioNoteSnapshot,
  type VoiceNoteObservation,
} from "./support/voice-operations";

type UsageCall = {
  callId: string;
  jobId: string;
  attempt: number;
  replayGeneration: number;
  comparisonMode: string;
  technicalOutcome: string;
  state: string;
  costState: string;
  reportedCostDecimal: string | null;
  audioDurationMs: number | null;
};
type UsageResponse = { data?: { llmUsage?: { calls: UsageCall[]; attemptedCalls: number; knownReportedCostDecimal: string; costComplete: boolean; unknownCostCalls: number } } };
type UsageData = NonNullable<NonNullable<UsageResponse["data"]>["llmUsage"]>;

async function waitForNote(page: Page, observation: VoiceNoteObservation, predicate: (note: AudioNoteSnapshot) => boolean, message: string, timeout = 90_000): Promise<AudioNoteSnapshot> {
  let current: AudioNoteSnapshot | undefined;
  await expect.poll(async () => {
    current = await getAudioNote(page, observation);
    return predicate(current);
  }, { timeout, message }).toBe(true);
  return current!;
}

async function openUsageForInspection(page: Page, inspectionId: string, onUsage: (usage: UsageData | undefined) => void): Promise<void> {
  page.on("response", async (response) => {
    if (!response.url().endsWith("/graphql") || !response.request().postData()?.includes("AdminLLMUsage")) return;
    try {
      const usage = ((await response.json()) as UsageResponse).data?.llmUsage;
      onUsage(usage);
    } catch { /* unrelated response */ }
  });
  if (new URL(page.url()).pathname !== "/llm-usage") await navigateAdmin(page, "Consumo de LLM");
  await page.locator(".llm-usage-advanced-filters > summary").click();
  await page.getByLabel("Tipo de consumo").selectOption("TRANSCRIPTION");
  await page.getByLabel("Modo").selectOption("MOCK");
  const from = page.getByLabel("Início");
  const to = page.getByLabel("Fim");
  const now = new Date();
  const localInput = (value: Date) => new Date(value.getTime() - value.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
  await from.fill(localInput(new Date(now.getTime() - 24 * 60 * 60_000)));
  await to.fill(localInput(new Date(now.getTime() + 60 * 60_000)));
  await page.getByLabel("Inspection ID").fill(inspectionId);
  await page.getByLabel("Incluir dados de teste").check();
  await page.getByRole("button", { name: "Aplicar" }).click();

  const tenants = page.getByRole("region", { name: "Consumo por tenants" });
  await tenants.getByRole("button", { name: "Abrir consumo de Minha operação", exact: true }).click();
  const inspections = page.getByRole("region", { name: "Consumo por vistorias" });
  const latestInspection = inspections.getByRole("button", { name: /^Abrir consumo de [0-9a-f-]{36}$/i }).first();
  await expect(latestInspection).toBeVisible({ timeout: 30_000 });
  await latestInspection.click();
  await expect(page.getByRole("list", { name: "Chamadas de LLM" })).toBeVisible();
  await expect(page.locator(".status-line")).not.toContainText("Carregando consumo");
}

test.describe("voice operations against the local stack", () => {
  test.describe.configure({ timeout: 240_000 });
  test.skip(process.env.INSPECTION_E2E_AUTH !== "true", "requer a stack local");

  test("E2E-019: filtra transcrições e confere sucesso, retry, custo parcial e duração do áudio", async ({ page, request, context }) => {
    test.setTimeout(240_000);
    test.skip(!["chromium", "desktop-chrome"].includes(test.info().project.name), "a fixture WebM/Opus usa o MediaRecorder nativo do Chromium");
    const assertRuntimeClean = installRuntimeGuards(page);
    await loginVoiceOperationsAdmin(page);

    const capture = await context.newPage();
    const seeded = seedVoiceCaptureURL("PHOTO");
    let transientMapping: string | undefined;
    let noCostMapping: string | undefined;
    try {
      await prepareRealVoiceRecorder(capture);
      await authenticateVoiceCapture(capture, request, seeded);
      const baselineNote = await recordRealVoiceNote(capture);
      await waitForNote(capture, baselineNote, (note) => note.status === "COMPLETED", "A transcrição de referência deve terminar antes da falha transitória");
      transientMapping = await installTransientTranscriptionMapping(request);
      const retryNote = await recordRealVoiceNote(capture);
      await waitForNote(capture, retryNote, (note) => note.status === "COMPLETED", "A nota com falha transitória deve concluir pelo retry automático");
      await removeWiremockMapping(request, transientMapping);
      transientMapping = undefined;

      noCostMapping = await installMissingCostTranscriptionMapping(request);
      const noCostNote = await recordRealVoiceNote(capture);
      await waitForNote(capture, noCostNote, (note) => note.status === "COMPLETED", "A nota sem header de custo deve concluir com transcrição");
      await removeWiremockMapping(request, noCostMapping);
      noCostMapping = undefined;

      let usage: UsageData | undefined;
      await openUsageForInspection(page, seeded.inspectionId, (next) => { usage = next; });
      await expect(page.getByLabel("Tipo de consumo")).toHaveValue("TRANSCRIPTION");
      await page.getByRole("button", { name: "Atualizar", exact: true }).click();
      await expect.poll(() => usage?.attemptedCalls, { timeout: 30_000, message: "A atualização deve carregar as quatro tentativas duráveis do ledger" }).toBe(4);
      const calls = page.getByRole("list", { name: "Chamadas de LLM" }).getByRole("listitem");
      await expect(calls).toHaveCount(4);
      for (let index = 0; index < 4; index++) await expect(calls.nth(index)).toContainText("Duração do áudio:");
      await expect(page.getByText(/cobertura de custo parcial/)).toBeVisible();
      expect(usage!.calls).toHaveLength(4);
      expect(new Set(usage!.calls.map((call) => call.callId)).size).toBe(4);
      expect(usage!.calls.every((call) => call.comparisonMode === "TRANSCRIPTION")).toBe(true);
      expect(usage!.calls.every((call) => (call.audioDurationMs ?? 0) >= 2_000)).toBe(true);
      expect(usage!.unknownCostCalls).toBeGreaterThanOrEqual(1);
      expect(usage!.costComplete).toBe(false);
      expect(usage!.knownReportedCostDecimal).not.toBe("0");
      const retryCalls = usage!.calls.filter((call) => usage!.calls.filter((candidate) => candidate.jobId === call.jobId).length > 1);
      expect(retryCalls).toHaveLength(2);
      expect(retryCalls.map((call) => call.attempt).sort()).toEqual([1, 2]);
      expect(new Set(retryCalls.map((call) => call.replayGeneration)).size).toBe(1);
      expect(retryCalls.filter((call) => /success|succeed/i.test(call.technicalOutcome))).toHaveLength(1);
      expect(usage!.calls.filter((call) => /success|succeed/i.test(call.technicalOutcome))).toHaveLength(3);

      const unknownCostCall = usage!.calls.find((call) => call.reportedCostDecimal === null);
      expect(unknownCostCall).toBeTruthy();
      await calls.filter({ hasText: "Custo desconhecido" }).first().getByRole("button", { name: /^Abrir detalhes da chamada/ }).click();
      const detail = page.getByRole("dialog", { name: /Detalhes da chamada/ });
      await expect(detail).toContainText(unknownCostCall!.callId);
      await expect(detail).toContainText("Duração do áudio");
      await expect(detail).toContainText("Não informado");
      await assertRuntimeClean();
    } finally {
      if (transientMapping) await removeWiremockMapping(request, transientMapping).catch(() => undefined);
      if (noCostMapping) await removeWiremockMapping(request, noCostMapping).catch(() => undefined);
      await capture.close().catch(() => undefined);
    }
  });

  test("E2E-020: pausa novas admissões, recupera a transcrição e o descarte do áudio", async ({ page, request, context }) => {
    test.setTimeout(240_000);
    test.skip(!["chromium", "desktop-chrome"].includes(test.info().project.name), "a fixture WebM/Opus usa o MediaRecorder nativo do Chromium");
    const assertRuntimeClean = installRuntimeGuards(page);
    await loginVoiceOperationsAdmin(page);
    const capture = await context.newPage();
    const seeded = seedVoiceCaptureURL("PHOTO");
    let mapping: string | undefined;
    let restoreStoragePolicy: (() => void) | undefined;
    let originalPolicy: Awaited<ReturnType<typeof configureVoiceAdmission>> | undefined;
    let cleanupFailure: AggregateError | undefined;
    try {
      await prepareRealVoiceRecorder(capture);
      await authenticateVoiceCapture(capture, request, seeded);
      mapping = await installTransientTranscriptionMapping(request);
      const note = await recordRealVoiceNote(capture);
      if (!note.clientNoteId) throw new Error("A admissão não retornou o identificador de armazenamento temporário.");
      await waitForNote(capture, note, (current) => current.status === "RETRY_WAIT", "A primeira tentativa deve registrar a falha transitória antes do retry");

      originalPolicy = await configureVoiceAdmission(page, false, (previous) => { originalPolicy = previous; });
      expect(originalPolicy.enabled).toBe(true);
      // This scoped S3 policy denies only this temporary audio object's DeleteObject. It leaves uploads, reads, other notes and buckets available.
      restoreStoragePolicy = denyAudioDeletionForNote("*", note.clientNoteId).restore;
      const completed = await waitForNote(capture, note, (current) => current.status === "COMPLETED", "A nota aceita deve concluir mesmo após pausar novas admissões", 150_000);
      expect(completed.revisions.filter((revision) => revision.kind === "MODEL")).toHaveLength(1);
      await expect(page.getByRole("status")).toContainText("Anotações por voz desativadas.");

      await waitForNote(capture, note, (current) => current.audioDeletionStatus === "FAILED", "O scheduler deve registrar a falha reversível de exclusão", 150_000);
      restoreStoragePolicy();
      restoreStoragePolicy = undefined;
      const cleaned = await waitForNote(capture, note, (current) => current.audioDeletionStatus === "DELETED" && current.audioAvailability !== "AVAILABLE", "O scheduler deve recuperar a exclusão após remover a política temporária", 180_000);
      expect(cleaned.status).toBe("COMPLETED");
      expect(cleaned.revisions.filter((revision) => revision.kind === "MODEL")).toHaveLength(1);

      await removeWiremockMapping(request, mapping);
      mapping = undefined;
      let usage: UsageData | undefined;
      await openUsageForInspection(page, seeded.inspectionId, (next) => { usage = next; });
      await expect.poll(() => usage?.calls.filter((call) => /success|succeed/i.test(call.technicalOutcome)).length, { timeout: 15_000, message: "A transcrição recuperada deve aparecer no ledger" }).toBe(1);
      const noteCalls = usage!.calls.filter((call) => call.jobId === usage!.calls.find((call) => /success|succeed/i.test(call.technicalOutcome))?.jobId);
      expect(noteCalls).toHaveLength(2);
      expect(noteCalls.map((call) => call.attempt).sort()).toEqual([1, 2]);
      expect(noteCalls.filter((call) => /success|succeed/i.test(call.technicalOutcome))).toHaveLength(1);
      await assertRuntimeClean();
    } finally {
      const cleanupErrors: unknown[] = [];
      try { restoreStoragePolicy?.(); } catch (error) { cleanupErrors.push(error); }
      try { if (mapping) await removeWiremockMapping(request, mapping); } catch (error) { cleanupErrors.push(error); }
      try { if (originalPolicy) await restoreVoiceAdmission(page, originalPolicy); } catch (error) { cleanupErrors.push(error); }
      try { await capture.close(); } catch (error) { cleanupErrors.push(error); }
      if (cleanupErrors.length) cleanupFailure = new AggregateError(cleanupErrors, "Falha ao restaurar os recursos temporários do E2E-020.");
    }
    if (cleanupFailure) throw cleanupFailure;
  });
});
