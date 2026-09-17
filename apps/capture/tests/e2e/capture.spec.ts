import { expect, test } from "@playwright/test";

test("E2E-053 Capture owns only its invitation route", async ({ page }) => {
  await page.route("**/graphql", async (route) => route.fulfill({ contentType: "application/json", body: JSON.stringify({ data: { requestInvitationOtp: { userErrors: [] } } }) }));
  const otpResponse = page.waitForResponse((response) => response.url().includes("graphql") && (response.request().postData() ?? "").includes("requestInvitationOtp"));
  await page.goto("/capture/invalid-link");
  await otpResponse;
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
});

test("E2E-054 Capture displays media progress while metadata waits for screening", async ({ page, browserName }) => {
  test.skip(browserName === "webkit", "The existing WebKit mobile harness does not dispatch the second GraphQL interaction after OTP; Android covers this interactive flow and WebKit retains the route smoke test.");
  await page.context().grantPermissions(["geolocation"]);
  await page.context().setGeolocation({ latitude: -27.4487, longitude: -48.428, accuracy: 35 });
  let metadataAttempts = 0;
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

  await page.route("**/upload-part", async (route) => route.fulfill({ status: 200, headers: { etag: "etag-1" } }));
  await page.route("**/*graphql*", async (route) => {
    const body = JSON.parse(route.request().postData() ?? "{}") as { query?: string };
    const query = body.query ?? "";
    if (query.includes("requestInvitationOtp")) return route.fulfill({ json: { data: { requestInvitationOtp: { status: "SENT", userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("verifyInvitationOtp")) return route.fulfill({ json: { data: { verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("externalCapture")) return route.fulfill({ json: { data: bootstrap } });
    if (query.includes("acceptProcessing")) return route.fulfill({ json: { data: { acceptProcessing: { status: "ACCEPTED", userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("createMediaUpload")) return route.fulfill({ json: { data: { createMediaUpload: { upload: { mediaId: "media-1", uploadId: "upload-1", expiresAt: "2099-01-01T00:00:00Z", partSizeBytes: 5 * 1024 * 1024 }, userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("presignMediaParts")) return route.fulfill({ json: { data: { presignMediaParts: { parts: [{ partNumber: 1, url: "http://localhost:3003/upload-part", expiresAt: "2099-01-01T00:00:00Z" }], userErrors: [], clientMutationId: "c" } } } });
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
  await expect(page.getByText("Escolher da galeria")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Adicionar descrição" })).toBeVisible();
  await page.getByLabel("Tirar foto").setInputFiles({ name: "overview.jpg", mimeType: "image/jpeg", buffer: Buffer.from("image") });

  await expect(page.getByRole("progressbar", { name: "Progresso do envio de Visão geral do imóvel" })).toBeVisible();
  await expect(page.getByText(/aguardando (envio|verificação)/i).first()).toBeVisible();
  await expect(page.getByRole("status").filter({ hasText: "Upload concluído" })).toBeVisible({ timeout: 15_000 });
  expect(metadataAttempts).toBe(2);
});
