import { expect, test } from "@playwright/test";

test.use({ serviceWorkers: "block" });

const referenceSVG = `<svg xmlns="http://www.w3.org/2000/svg" width="640" height="480" viewBox="0 0 640 480"><rect width="640" height="480" fill="#d8e9e6"/><rect x="80" y="140" width="480" height="260" fill="#f5eee0"/><text x="320" y="270" text-anchor="middle" font-size="40">Referência</text></svg>`;
const photo = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/nwAAAABJRU5ErkJggg==", "base64");
const replacementPhoto = Buffer.concat([photo, Buffer.from([1])]);

test("E2E-030; E2E-041 guided comparison shows each reference, previews photos, and advances after upload", async ({ page }) => {
  await page.context().grantPermissions(["geolocation"]);
  await page.context().setGeolocation({ latitude: -27.4487, longitude: -48.428, accuracy: 35 });
  let createdUploads = 0;
  const replacementTargets: Array<string | undefined> = [];
  let completedAnswers: Array<{ requirementKey: string; mediaIds: string[] }> = [];
  const keys = ["origin:reference-1", "origin:reference-2"];
  const bootstrap = {
    responsibilityId: "responsibility-guided", recaptureRequestId: null, status: "OPEN", confirmationOnly: false, kind: "INSPECTION", disclosureVersion: "v1",
    reference: { comparisonMode: "FIXED_ORIGIN" }, policy: { allowGallery: false, gpsRequired: false, geofenceMeters: 0 },
    referenceItems: keys.map((key, index) => ({ mediaId: `reference-${index + 1}`, requirementKey: key, description: index === 0 ? "Cozinha" : "Sala", availability: "AVAILABLE", imageUrl: `/mock-reference-${index + 1}.svg`, imageUrlExpiresAt: "2099-01-01T00:00:00Z" })),
    requirements: keys.map((key, index) => ({ key, section: "Referência", label: "Referência", instructions: index === 0 ? "Cozinha" : "Sala", required: true, minimumMedia: 1, maximumMedia: 1, descriptionRequired: false, captureSourcePolicy: "CAMERA_DEFAULT", comparisonTarget: "FIXED_ORIGIN", impossibilityAllowed: true })),
    answers: []
  };

  await page.route("**/mock-reference-*.svg", (route) => route.fulfill({ contentType: "image/svg+xml", body: referenceSVG }));
  await page.route("**/upload-part", (route) => route.fulfill({ status: 200, headers: { etag: "etag-1" } }));
  await page.route("**/*graphql*", (route) => {
    const body = JSON.parse(route.request().postData() ?? "{}") as { query?: string; variables?: { input?: { mediaId?: string; requirementKey?: string; replacesMediaId?: string } } };
    const query = body.query ?? "";
    if (query.includes("requestInvitationOtp")) return route.fulfill({ json: { data: { requestInvitationOtp: { status: "SENT", userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("verifyInvitationOtp")) return route.fulfill({ json: { data: { verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("externalCapture")) return route.fulfill({ json: { data: { externalCapture: { ...bootstrap, answers: completedAnswers } } } });
    if (query.includes("acceptProcessing")) return route.fulfill({ json: { data: { acceptProcessing: { status: "ACCEPTED", userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("createMediaUpload")) { createdUploads += 1; return route.fulfill({ json: { data: { createMediaUpload: { upload: { mediaId: `media-${createdUploads}`, uploadId: `upload-${createdUploads}`, expiresAt: "2099-01-01T00:00:00Z", partSizeBytes: 5 * 1024 * 1024 }, userErrors: [], clientMutationId: "c" } } } }); }
    if (query.includes("presignMediaParts")) return route.fulfill({ json: { data: { presignMediaParts: { parts: [{ partNumber: 1, url: "/upload-part", expiresAt: "2099-01-01T00:00:00Z" }], userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("completeMediaUpload")) return route.fulfill({ json: { data: { completeMediaUpload: { media: { id: `media-${createdUploads}`, status: "READY" }, userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("saveCaptureMetadata")) {
      const requirementKey = body.variables?.input?.requirementKey ?? keys[createdUploads - 1];
      const mediaId = body.variables?.input?.mediaId ?? "media-" + createdUploads;
      replacementTargets.push(body.variables?.input?.replacesMediaId);
      completedAnswers = [...completedAnswers.filter((answer) => answer.requirementKey !== requirementKey), { requirementKey, mediaIds: [mediaId] }];
      return route.fulfill({ json: { data: { saveCaptureMetadata: { media: { id: mediaId, status: "READY", replacesMediaId: body.variables?.input?.replacesMediaId ?? null }, userErrors: [], clientMutationId: "c" } } } });
    }
    return route.fulfill({ json: { data: {} } });
  });

  const otp = page.waitForResponse((response) => response.url().includes("graphql") && (response.request().postData() ?? "").includes("requestInvitationOtp"));
  await page.goto("/capture/guided-test-link");
  await otp;
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel("Processamento das fotos").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();

  await expect(page.getByRole("heading", { name: "Cozinha", exact: true })).toBeVisible();
  await expect(page.getByText("Fotografe o mesmo local usando a imagem como guia.")).toBeVisible();
  await expect(page.getByText("Evidências · Foto 1 de 2")).toBeVisible();
  await expect(page.getByRole("img", { name: "Foto de referência: Cozinha" })).toBeVisible();
  await expect(page.getByRole("combobox", { name: "Requisito" })).toHaveCount(0);
  const photoPanel = page.locator(".capture-photo-panel");
  await expect(photoPanel.getByRole("button", { name: "Tocar ou clicar para tirar a foto" })).toBeVisible();
  await expect(photoPanel.getByRole("button", { name: "Tocar ou clicar para tirar a foto" })).toBeInViewport();
  await expect(photoPanel.getByRole("button", { name: "Tirar foto", exact: true })).toHaveCount(0);
  await expect(page.locator(".capture-camera-action")).toHaveCount(0);

  const primaryChooser = page.waitForEvent("filechooser");
  await photoPanel.getByRole("button", { name: "Tocar ou clicar para tirar a foto" }).click();
  await (await primaryChooser).setFiles({ name: "cozinha.png", mimeType: "image/png", buffer: photo });
  await expect(page.getByRole("img", { name: "Prévia da sua foto de comparação" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Confira sua foto" })).toBeVisible();
  expect(createdUploads).toBe(0);
  const canceledChooser = page.waitForEvent("filechooser");
  await photoPanel.getByRole("button", { name: "Tocar ou clicar na foto para substituí-la" }).click();
  await (await canceledChooser).setFiles([]);
  await expect(page.getByRole("img", { name: "Prévia da sua foto de comparação" })).toBeVisible();
  expect(createdUploads).toBe(0);
  const confirmPhoto = photoPanel.getByRole("button", { name: "Confirmar foto" });
  await expect(confirmPhoto).toBeInViewport();
  await expect(confirmPhoto).toHaveAttribute("data-tooltip", "Confirmar foto");
  const confirmBounds = await confirmPhoto.boundingBox();
  expect(confirmBounds?.width).toBeGreaterThanOrEqual(44);
  expect(confirmBounds?.height).toBeGreaterThanOrEqual(44);
  await confirmPhoto.hover();
  await expect.poll(() => confirmPhoto.evaluate((element) => getComputedStyle(element, "::after").opacity)).toBe("1");
  await confirmPhoto.focus();
  await confirmPhoto.press("Enter");
  await expect(page.getByText("Foto 2 de 2")).toBeVisible({ timeout: 15_000 });
  await expect(page.getByRole("img", { name: "Foto de referência: Sala" })).toBeVisible();
  expect(createdUploads).toBe(1);
  await page.getByRole("navigation", { name: "Fotos de referência" }).getByRole("button", { name: /Cozinha/ }).click();
  await expect(page.getByRole("img", { name: "Sua foto: Cozinha" })).toBeVisible();
  await expect(page.getByText("Foto registrada neste dispositivo.", { exact: true })).toBeVisible();
  await page.getByRole("navigation", { name: "Fotos de referência" }).getByRole("button", { name: /Sala/ }).click();
  await page.getByLabel("Tirar ou substituir foto").setInputFiles({ name: "sala.png", mimeType: "image/png", buffer: photo });
  await page.getByRole("button", { name: "Confirmar foto" }).click();
  await expect(page.getByLabel("Resumo da captura")).toHaveText("2/2", { timeout: 15_000 });
  expect(createdUploads).toBe(2);
  await page.getByRole("navigation", { name: "Fotos de referência" }).getByRole("button", { name: /Cozinha/ }).click();
  await expect(photoPanel.getByRole("button", { name: "Tocar ou clicar na foto para substituí-la" })).toBeVisible();
  const replacementChooser = page.waitForEvent("filechooser");
  await photoPanel.getByRole("button", { name: "Tocar ou clicar na foto para substituí-la" }).click();
  await (await replacementChooser).setFiles({ name: "cozinha-nova.png", mimeType: "image/png", buffer: replacementPhoto });
  await page.getByRole("button", { name: "Confirmar foto" }).click();
  await expect(page.getByLabel("Resumo da captura")).toHaveText("2/2", { timeout: 15_000 });
  await expect.poll(() => createdUploads).toBe(3);
  await expect.poll(() => replacementTargets.length).toBe(3);
  expect(replacementTargets).toEqual([undefined, undefined, "media-1"]);
  await page.reload();
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel("Processamento das fotos").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByRole("img", { name: "Sua foto: Cozinha" })).toBeVisible();
  await expect(photoPanel.getByRole("button", { name: "Tocar ou clicar na foto para substituí-la" })).toBeVisible();
  await page.getByRole("button", { name: "Revisar vistoria" }).click();
  await expect(page.getByRole("region", { name: "Evidências" })).toBeVisible();
  await expect(page.getByText("Concluída", { exact: true })).toHaveCount(2);
});
