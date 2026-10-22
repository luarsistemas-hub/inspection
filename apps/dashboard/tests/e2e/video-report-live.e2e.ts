import { expect, test } from "@playwright/test";
import {
  LocalGraphQL,
  activateOrigin,
  apiURL,
  assertLiveAI,
  captureBaseURL,
  createInspectionFromDashboard,
  createOriginInvitation,
  createVideoScenario,
  diagnostics,
  mailpitIDs,
  openReportAndCheckPDF,
  preflight,
  uploadAndSubmitCapture,
  waitForPreparation,
  waitForReport,
  originPath,
  currentPath,
  type VideoScenario,
} from "./support/video-live";

test.setTimeout(Number(process.env.PLAYWRIGHT_VIDEO_LIVE_TIMEOUT_MS ?? 30 * 60 * 1000));
test.skip(process.env.INSPECTION_E2E_VIDEO_LIVE !== "true", "Suíte live com IA real: execute com npm run test:e2e:video-live.");

test.describe("E2E de laudos de vídeo com IA live", () => {
  test("checklist cria um laudo com frames atuais analisados pela IA", async ({ page, request }) => {
    await preflight(false);
    const api = new LocalGraphQL(request, page);
    const scenario = {} as VideoScenario;
    const evidence: Record<string, unknown> = {};
    const capture = await page.context().newPage();
    try {
      await api.login("/inspections");
      Object.assign(scenario, await createVideoScenario(api));
      const invitationSnapshot = await mailpitIDs(request);
      await createInspectionFromDashboard(page, api, scenario, "CHECKLIST_ONLY");

      const uploaded = await uploadAndSubmitCapture(capture, request, invitationSnapshot, scenario.email, currentPath, api);
      scenario.currentMediaId = uploaded.mediaId;
      scenario.currentPreparationId = uploaded.preparationId;
      const report = await waitForReport(api, scenario.inspectionId!);
      const requirements = report.report.requirements as Array<{ analysisMode: string; analysisStatus: string }>;
      expect(requirements.length).toBeGreaterThan(0);
      expect(requirements.every((item) => item.analysisMode === "CURRENT_ONLY" && item.analysisStatus === "COMPLETED"), JSON.stringify(requirements)).toBe(true);
      expect(report.evidence.some((item) => item.role === "CURRENT" && item.sourceMediaId === scenario.currentMediaId && item.preparationId === scenario.currentPreparationId), "o laudo associa frames atuais ao vídeo e à preparação desta execução").toBe(true);
      expect(report.evidence.some((item) => item.role === "REFERENCE"), "checklist não deve criar frames de referência").toBe(false);
      evidence.report = { id: report.id, requirements };
      evidence.evidence = report.evidence.map(({ url: _url, ...item }) => item);
      evidence.llmCalls = await assertLiveAI(api, scenario.inspectionId!, ["VIDEO_SCOPE", "CURRENT_ONLY"]);
      await openReportAndCheckPDF(page, request, api, scenario, report, test.info());
    } catch (error) {
      await diagnostics(test.info(), page, scenario, { stage: "checklist", error: (error as Error).message, evidence, captureOrigin: new URL(capture.url()).origin, apiURL, captureBaseURL }, api);
      throw error;
    } finally {
      await capture.close();
    }
  });

  test("comparação analisa frames de origem e atuais e gera o laudo", async ({ page, request }) => {
    await preflight(true);
    const api = new LocalGraphQL(request, page);
    const scenario = {} as VideoScenario;
    const evidence: Record<string, unknown> = {};
    const capture = await page.context().newPage();
    try {
      await api.login("/inspections");
      Object.assign(scenario, await createVideoScenario(api));
      const origin = await createOriginInvitation(api, scenario, request);
      const originUpload = await uploadAndSubmitCapture(capture, request, origin.messages, scenario.email, originPath, api);
      scenario.originMediaId = originUpload.mediaId;
      scenario.originPreparationId = originUpload.preparationId;

      await expect.poll(async () => {
        const result = await api.execute<{ originVersions: { nodes: Array<{ id: string; status: string; preparationStatus: string | null; videoPreparationId: string | null; captureSubmitted: boolean }> } }>(
          `query VideoE2EOrigin($assetId: ID!) { originVersions(assetId: $assetId, first: 100) { nodes { id status preparationStatus videoPreparationId captureSubmitted } } }`,
          { assetId: scenario.assetId });
        const version = result.originVersions.nodes.find((item) => item.id === scenario.originVersionId);
        if (version?.preparationStatus === "FAILED") throw new Error(`Preparação do vídeo de origem falhou: ${JSON.stringify(version)}`);
        // A versão só recebe videoPreparationId ao ser ativada, então o vínculo é conferido depois.
        return version?.preparationStatus === "READY" && version.captureSubmitted;
      }, { timeout: 9 * 60_000, intervals: [1_000, 3_000, 5_000], message: "vídeo de origem preparado para ativação" }).toBe(true);
      await activateOrigin(api, scenario);
      const activated = await api.execute<{ originVersions: { nodes: Array<{ id: string; videoPreparationId: string | null }> } }>(
        `query VideoE2EActivatedOrigin($assetId: ID!) { originVersions(assetId: $assetId, first: 100) { nodes { id videoPreparationId } } }`,
        { assetId: scenario.assetId });
      expect(activated.originVersions.nodes.find((item) => item.id === scenario.originVersionId)?.videoPreparationId, "origem ativada aponta para a preparação desta execução").toBe(scenario.originPreparationId);

      const currentInvitation = await mailpitIDs(request);
      await createInspectionFromDashboard(page, api, scenario, "FIXED_ORIGIN");
      const currentUpload = await uploadAndSubmitCapture(capture, request, currentInvitation, scenario.email, currentPath, api);
      scenario.currentMediaId = currentUpload.mediaId;
      scenario.currentPreparationId = currentUpload.preparationId;

      const report = await waitForReport(api, scenario.inspectionId!);
      const requirements = report.report.requirements as Array<{ analysisMode: string; analysisStatus: string }>;
      expect(requirements.some((item) => item.analysisMode === "COMPARE_ORIGIN_CURRENT" && item.analysisStatus === "COMPLETED"), JSON.stringify(requirements)).toBe(true);
      expect(report.evidence.some((item) => item.role === "REFERENCE" && item.sourceMediaId === scenario.originMediaId && item.preparationId === scenario.originPreparationId), "o laudo contém frames da origem que foi preparada nesta execução").toBe(true);
      expect(report.evidence.some((item) => item.role === "CURRENT" && item.sourceMediaId === scenario.currentMediaId && item.preparationId === scenario.currentPreparationId), "o laudo contém frames atuais da vistoria desta execução").toBe(true);
      evidence.report = { id: report.id, requirements };
      evidence.evidence = report.evidence.map(({ url: _url, ...item }) => item);
      evidence.llmCalls = await assertLiveAI(api, scenario.inspectionId!, ["VIDEO_SCOPE", "VIDEO_MATCH", "VIDEO_VERIFY", "COMPARE_ORIGIN_CURRENT"]);
      await openReportAndCheckPDF(page, request, api, scenario, report, test.info());
    } catch (error) {
      await diagnostics(test.info(), page, scenario, { stage: "comparação", error: (error as Error).message, evidence, captureOrigin: new URL(capture.url()).origin, apiURL, captureBaseURL }, api);
      throw error;
    } finally {
      await capture.close();
    }
  });
});
