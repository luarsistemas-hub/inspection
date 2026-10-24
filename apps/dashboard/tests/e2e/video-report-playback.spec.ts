import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { expect, test, type Page } from "@playwright/test";

test.skip(process.env.INSPECTION_E2E_MOCKS !== "true", "run with INSPECTION_E2E_MOCKS=true and the local identity stack");
test.use({ channel: async ({ browserName }, use) => { await use(browserName === "chromium" ? "chrome" : undefined); } });

test.beforeEach(async ({ page }) => {
  await page.route("**/protocol/openid-connect/token", (route) => route.fulfill({
    status: 200,
    contentType: "application/json",
    headers: { "access-control-allow-origin": "*" },
    body: JSON.stringify({ access_token: "playback-test-token" }),
  }));
  await page.addInitScript(() => sessionStorage.setItem("inspection.dashboard.pkce", JSON.stringify({ verifier: "playback-test-verifier", state: "playback-test-state", returnTo: "/reports" })));

  const requirements = ["Cozinha", "Banheiro"].map((section, index) => ({ key: `video:${index}`, section, label: section, instructions: null, coverage: "COMPLETE", impossibilityReason: null, noRelevantChange: null, analysisMode: "COMPLETED", analysisStatus: "COMPLETED" }));
  const evidence = requirements.map((requirement, index) => ({ id: `evidence-${index}`, requirementKey: requirement.key, role: "CURRENT", description: "Pia", captureSource: "VIDEO", capturedAt: null, displayDigest: "digest", availability: "AVAILABLE", sourceAvailability: "AVAILABLE", evidenceKind: "VIDEO_FRAME", frameId: `frame-${index}`, timestampMs: 10_000, ptsMicros: "10000000", preparationId: "prep-1", planId: "plan-1", sourceMediaId: "source-1gb", component: "Pia", label: "Pia", flags: [], url: null }));
  const report = {
    id: "report-video", inspectionId: "inspection-video", projectId: null, version: 1, mode: "VIDEO", classification: "ATTENTION", jsonDigest: "digest", htmlDigest: "digest", canonicalJSON: {}, html: "", createdAt: "2030-01-01T00:00:00Z", advisory: "Limitação da reprodução de vídeo.", pdfStatus: "READY",
    context: { asset: { id: "asset-1", name: "Imóvel QA", externalKey: "QA-1", address: "Rua QA" }, participant: { id: "person-1", name: "QA" }, template: { id: "template-1", name: "Padrão", version: 1 }, inspection: { projectId: null, stageId: null, stageLabel: "Entrada", dueAt: null, submittedAt: null, generatedAt: "2030-01-01T00:00:00Z" } },
    requirements, evidence: { nodes: evidence, pageInfo: { endCursor: null, hasNextPage: false } }, findings: [], timeline: [], publication: null,
  };

  await page.route("**/graphql", async (route) => {
    const query = route.request().postData() ?? "";
    const operation = query.match(/(?:query|mutation)\s+(\w+)/)?.[1];
    const data = operation === "DashboardMemberships" || operation === "DashboardGate"
      ? { me: { identityId: "identity-1", tenantId: "tenant-1", productEntitlements: ["DASHBOARD"], roles: ["MANAGER"], effectiveScopes: [{ kind: "Tenant", resourceId: "tenant-1" }], memberships: [{ id: "membership-1", tenantId: "tenant-1", role: "MANAGER", status: "ACTIVE" }] }, tenant: { id: "tenant-1", name: "QA", status: "ACTIVE" } }
      : operation === "Reports" ? { reports: { nodes: [{ id: "report-video", inspectionId: "inspection-video", assetName: "Imóvel QA", assetAddress: "Rua QA", assetExternalKey: "QA-1", participantName: "QA", generatedAt: "2030-01-01T00:00:00Z", classification: "ATTENTION", version: 1 }], pageInfo: { endCursor: null, hasNextPage: false } } }
        : operation === "ReportWorkspace" ? { report } : {};
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ data }) });
  });
  await page.route("**/api/media/videos/session", (route) => route.fulfill({ status: 204, headers: { "set-cookie": "inspection_video_playback=qa; Path=/; SameSite=Lax" } }));
});

test("fecha o modal de vídeo enquanto o stream está pendente", async ({ page }) => {
  let streamStarted!: () => void;
  const streamStart = new Promise<void>((resolve) => { streamStarted = resolve; });
  await page.route("**/api/media/videos/source-1gb/content?*", async (route) => {
    expect(new URL(route.request().url()).searchParams.get("snapshotId")).toBe("report-video");
    streamStarted();
    await new Promise((resolve) => setTimeout(resolve, 3_000));
    await route.fulfill({ status: 206, contentType: "video/mp4", headers: { "content-range": "bytes 0-32767/1073741824", "accept-ranges": "bytes" }, body: Buffer.alloc(32 * 1024) });
  });

  const dialog = await openVideoEvidence(page);
  await dialog.getByRole("button", { name: /Reproduzir vídeo/ }).click();
  const video = page.locator(".report-video-dialog video");
  await expect(video).toBeVisible();
  await streamStart;
  await page.locator(".report-video-dialog").getByRole("button", { name: "Fechar vídeo" }).click();
  await expect(video).toBeHidden();
  await expect(dialog.getByRole("button", { name: "Banheiro" })).toBeVisible();
});

test("o play do par abre modal, inicia no ponto correto e omite identificadores técnicos", async ({ page }) => {
  const fixture = resolve("tests/fixtures/video/atual.mp4");
  test.skip(!existsSync(fixture), "Prepare atual.mp4 conforme tests/fixtures/video/README.md");
  const bytes = readFileSync(fixture);
  await page.route("**/api/media/videos/source-1gb/content?*", async (route) => {
    expect(new URL(route.request().url()).searchParams.get("snapshotId")).toBe("report-video");
    const range = route.request().headers().range?.match(/^bytes=(\d+)-(\d*)$/);
    const start = Number(range?.[1] ?? 0);
    const end = Math.min(Number(range?.[2] || bytes.length - 1), bytes.length - 1);
    await route.fulfill({ status: range ? 206 : 200, contentType: "video/mp4", headers: { "accept-ranges": "bytes", ...(range ? { "content-range": `bytes ${start}-${end}/${bytes.length}` } : {}) }, body: bytes.subarray(start, end + 1) });
  });
  const dialog = await openVideoEvidence(page);
  await expect(dialog).not.toContainText(/frame-[0-9]|PTS/);
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(await page.evaluate(() => window.innerWidth));
  const playButton = dialog.getByRole("button", { name: /Reproduzir vídeo/ });
  await playButton.click();
  const video = page.locator(".report-video-dialog video");
  await expect(video).toBeVisible();
  await expect.poll(() => video.evaluate((element: HTMLVideoElement) => !element.paused && element.currentTime >= 10), { timeout: 15_000 }).toBe(true);
  await video.evaluate((element: HTMLVideoElement) => element.pause());
  await page.keyboard.press("Escape");
  await expect(video).toBeHidden();
  await expect(playButton).toBeFocused();
});

async function openVideoEvidence(page: Page) {
  // Complete the mocked OIDC callback directly, as the other mocked specs do.
  await page.goto("/auth/callback?code=playback-test-code&state=playback-test-state");
  await expect(page.getByRole("navigation", { name: "Painel" })).toBeVisible();
  await page.getByRole("button", { name: /Abrir detalhes do laudo/ }).click();
  const dialog = page.getByRole("dialog", { name: "Laudo de vistoria" });
  await dialog.getByRole("button", { name: "Cozinha" }).click();
  await dialog.getByLabel("Ver evidências de Cozinha", { exact: true }).click();
  return dialog;
}
