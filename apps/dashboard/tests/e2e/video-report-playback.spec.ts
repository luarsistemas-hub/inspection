import { expect, test } from "@playwright/test";

test.skip(process.env.INSPECTION_E2E_MOCKS !== "true", "run with INSPECTION_E2E_MOCKS=true and the local identity stack");

test("IT-286 mantém a navegação do laudo responsiva enquanto o stream de vídeo demora", async ({ page }) => {
  await page.route("**/protocol/openid-connect/token", (route) => route.fulfill({
    status: 200,
    contentType: "application/json",
    headers: { "access-control-allow-origin": "*" },
    body: JSON.stringify({ access_token: "playback-test-token" }),
  }));
  await page.addInitScript(() => sessionStorage.setItem("inspection.dashboard.pkce", JSON.stringify({ verifier: "playback-test-verifier", state: "playback-test-state", returnTo: "/reports" })));

  const requirements = ["Cozinha", "Banheiro"].map((section, index) => ({ key: `video:${index}`, section, label: section, instructions: null, coverage: "COMPLETE", impossibilityReason: null, noRelevantChange: null, analysisMode: "COMPLETED", analysisStatus: "COMPLETED" }));
  const evidence = requirements.map((requirement, index) => ({ id: `evidence-${index}`, requirementKey: requirement.key, role: "CURRENT", description: `Frame ${requirement.section}`, captureSource: "VIDEO", capturedAt: null, displayDigest: "digest", availability: "AVAILABLE", sourceAvailability: "AVAILABLE", evidenceKind: "VIDEO_FRAME", frameId: `frame-${index}`, timestampMs: 10_000, ptsMicros: "10000000", preparationId: "prep-1", planId: "plan-1", sourceMediaId: "source-1gb", component: "Pia", label: "Pia", flags: [], url: null }));
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
  let streamFulfilled = false;
  let streamStarted!: () => void;
  const streamStart = new Promise<void>((resolve) => { streamStarted = resolve; });
  await page.route("**/api/media/videos/source-1gb/content", async (route) => {
    streamStarted();
    // A 32 KiB range chunk delayed by three seconds models a slow 1 GiB source.
    await new Promise((resolve) => setTimeout(resolve, 3_000));
    streamFulfilled = true;
    await route.fulfill({ status: 206, contentType: "video/mp4", headers: { "content-range": "bytes 0-32767/1073741824", "accept-ranges": "bytes" }, body: Buffer.alloc(32 * 1024) });
  });

  // Complete the mocked OIDC callback directly, as the other mocked specs do.
  await page.goto("/auth/callback?code=playback-test-code&state=playback-test-state");
  await expect(page.getByRole("navigation", { name: "Painel" })).toBeVisible();
  await page.getByRole("button", { name: /Abrir detalhes do laudo/ }).click();
  const dialog = page.getByRole("dialog", { name: "Laudo de vistoria" });
  await dialog.getByRole("button", { name: /Reproduzir vídeo/ }).click();
  const video = dialog.locator("video");
  await expect(video).toBeVisible();
  await video.evaluate((element: HTMLVideoElement) => { void element.play().catch(() => undefined); });
  await streamStart;
  await expect(dialog.getByRole("button", { name: /Banheiro/ })).toBeVisible();
  await dialog.getByRole("button", { name: /Banheiro/ }).click();
  await expect(dialog.getByText("Frame Banheiro")).toBeVisible();
  // Navigation completed while the slow stream was still pending.
  expect(streamFulfilled).toBe(false);
});
