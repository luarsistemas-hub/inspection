import { expect, test } from "@playwright/test";

test.use({ serviceWorkers: "block" });

test("Capture reaches the OTP boundary on its invitation route", async ({ page }) => {
  await page.route("**/graphql", async (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: { requestInvitationOtp: { userErrors: [] } } }) }));
  const otpResponse = page.waitForResponse((response) => response.url().includes("graphql") && (response.request().postData() ?? "").includes("requestInvitationOtp"));
  await page.goto("/capture/invalid-link");
  await otpResponse;
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
});

test("IT-153; IT-155; IT-194; E2E-031; E2E-054 persisted evidence resumes once and waits for server screening", async ({ page }) => {
  await page.context().grantPermissions(["geolocation"]);
  await page.context().setGeolocation({ latitude: -27.4487, longitude: -48.428, accuracy: 35 });
  let metadataAttempts = 0;
  let uploadAttempts = 0;
  const bootstrap = {
    externalCapture: {
      responsibilityId: "responsibility-1",
      recaptureRequestId: null,
      status: "OPEN",
      confirmationOnly: false,
      kind: "INSPECTION",
      disclosureVersion: "v1",
      reference: {},
      policy: { allowGallery: true, gpsRequired: false, geofenceMeters: 0 },
      requirements: [{ key: "overview", section: "property", label: "Overview", instructions: "Faça uma foto geral.", required: true, minimumMedia: 1, maximumMedia: 2, descriptionRequired: false, captureSourcePolicy: "CAMERA_DEFAULT", comparisonTarget: "FIXED_ORIGIN", impossibilityAllowed: false }],
      answers: []
    }
  };

  await page.route("**/upload-part", async (route) => {
    uploadAttempts += 1;
    if (uploadAttempts === 1) return route.abort("connectionreset");
    return route.fulfill({ status: 200, headers: { etag: "etag-1" } });
  });
  await page.route("**/*graphql*", async (route) => {
    const body = JSON.parse(route.request().postData() ?? "{}") as { query?: string };
    const query = body.query ?? "";
    if (query.includes("requestInvitationOtp")) return route.fulfill({ json: { data: { requestInvitationOtp: { status: "SENT", userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("verifyInvitationOtp")) return route.fulfill({ json: { data: { verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("externalCapture")) return route.fulfill({ json: { data: bootstrap } });
    if (query.includes("acceptProcessing")) return route.fulfill({ json: { data: { acceptProcessing: { status: "ACCEPTED", userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("createMediaUpload")) return route.fulfill({ json: { data: { createMediaUpload: { upload: { mediaId: "media-1", uploadId: "upload-1", expiresAt: "2099-01-01T00:00:00Z", partSizeBytes: 5 * 1024 * 1024 }, userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("presignMediaParts")) return route.fulfill({ json: { data: { presignMediaParts: { parts: [{ partNumber: 1, url: "/upload-part", expiresAt: "2099-01-01T00:00:00Z" }], userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("completeMediaUpload")) return route.fulfill({ json: { data: { completeMediaUpload: { media: { id: "media-1", status: "VERIFIED" }, userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("saveCaptureMetadata")) {
      metadataAttempts += 1;
      if (metadataAttempts === 1) return route.fulfill({ json: { errors: [{ message: "media verification and screening are pending", extensions: { code: "INVALID_STATE", field: "mediaId" } }] } });
      return route.fulfill({ json: { data: { saveCaptureMetadata: { media: { id: "media-1", status: "READY" }, userErrors: [], clientMutationId: "c" } } } });
    }
    return route.fulfill({ json: { data: {} } });
  });

  const captureOtpResponse = page.waitForResponse((response) => response.url().includes("graphql") && (response.request().postData() ?? "").includes("requestInvitationOtp"));
  await page.goto("/capture/invalid-link");
  await captureOtpResponse;
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await expect(page.getByLabel("Código de seis dígitos")).toHaveValue("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel("Processamento das fotos").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByRole("button", { name: "Escolher da galeria" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Adicionar descrição" })).toBeVisible();
  const photo = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/nwAAAABJRU5ErkJggg==", "base64");
  const photoInput = page.locator(".capture-camera-action input[type=file]");
  await photoInput.setInputFiles({ name: "overview.png", mimeType: "image/png", buffer: photo });

  await expect(page.getByRole("img", { name: "Prévia da sua foto de comparação" })).toBeVisible();
  await page.getByRole("button", { name: "Ativar tema escuro" }).click();
  await expect(page.getByRole("img", { name: "Prévia da sua foto de comparação" })).toBeVisible();
  await page.getByRole("button", { name: "Usar esta foto" }).click();
  await expect(page.getByRole("progressbar", { name: "Progresso do envio de Visão geral do imóvel" })).toBeVisible();
  await expect(page.getByText(/aguardando (envio|verificação)/i).first()).toBeVisible();
  await expect(page.getByRole("button", { name: "Retomar envio" })).toBeVisible();
  await page.getByRole("button", { name: "Retomar envio" }).dblclick();
  await expect(page.getByRole("status").filter({ hasText: "Upload concluído" })).toBeVisible({ timeout: 15_000 });
  expect(metadataAttempts).toBe(2);
  expect(uploadAttempts).toBe(2);
});
