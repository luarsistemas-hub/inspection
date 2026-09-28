import { expect, test, type Page } from "@playwright/test";

type CaptureScenario = {
  confirmationOnly?: boolean;
  requiredAnswer?: boolean;
  submitStatus?: "COMPLETED" | "EXPIRED";
  delayAccept?: boolean;
  screenedMedia?: boolean;
  allowGallery?: boolean;
  captureSourcePolicy?: string;
  rateLimitedOtp?: boolean;
  invalidOtp?: boolean;
  delayVerify?: boolean;
  bootstrapStatus?: string;
  expireOnAccept?: boolean;
  delaySubmit?: boolean;
  lostSubmitResponse?: boolean;
  rejectedUpload?: boolean;
  gpsRequired?: boolean;
  maximumMedia?: number;
};

async function mockCapture(page: Page, scenario: CaptureScenario = {}) {
  let status = scenario.bootstrapStatus ?? "OPEN";
  let acceptCalls = 0;
  let submitCalls = 0;
  let verifyCalls = 0;
  let bootstrapCalls = 0;
  let metadataSource: string | undefined;
  let replacedMediaId: string | null | undefined;
  let impossibleReason: string | null = null;
  await page.route("**/*graphql*", async (route) => {
    const body = JSON.parse(route.request().postData() ?? "{}") as { query?: string };
    const query = body.query ?? "";
    if (query.includes("requestInvitationOtp")) return route.fulfill({ json: { data: { requestInvitationOtp: { status: scenario.rateLimitedOtp ? "REJECTED" : "SENT", userErrors: scenario.rateLimitedOtp ? [{ code: "RATE_LIMITED", field: null, message: "retry later" }] : [], clientMutationId: "c" } } } });
    if (query.includes("verifyInvitationOtp")) {
      verifyCalls += 1;
      if (scenario.delayVerify) await new Promise((resolve) => setTimeout(resolve, 150));
      return route.fulfill({ json: { data: { verifyInvitationOtp: { status: scenario.invalidOtp ? "REJECTED" : "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: scenario.invalidOtp ? [{ code: "INVALID_INPUT", field: "code", message: "invalid" }] : [], clientMutationId: "c" } } } });
    }
    if (query.includes("externalCapture")) {
      bootstrapCalls += 1;
      return route.fulfill({ json: { data: { externalCapture: {
      responsibilityId: "responsibility-migration", recaptureRequestId: null, status, confirmationOnly: scenario.confirmationOnly ?? false,
      kind: "INSPECTION", disclosureVersion: "v1", reference: {}, policy: { allowGallery: scenario.allowGallery ?? true, gpsRequired: scenario.gpsRequired ?? false, geofenceMeters: 0 },
      requirements: [{ key: "front", section: "property", label: "Fachada", instructions: "Fotografe a fachada.", required: true, minimumMedia: 1, maximumMedia: scenario.maximumMedia ?? 1, descriptionRequired: false, captureSourcePolicy: scenario.captureSourcePolicy ?? "CAMERA_DEFAULT", comparisonTarget: "CHECKLIST_ONLY", impossibilityAllowed: true }],
      answers: scenario.requiredAnswer ? [{ requirementKey: "front", mediaIds: ["ready-media"], impossibilityReason: null, version: 1 }] : impossibleReason ? [{ requirementKey: "front", mediaIds: [], impossibilityReason: impossibleReason, version: 1 }] : scenario.screenedMedia ? [{ requirementKey: "front", mediaIds: ["screened-media"], impossibilityReason: null, version: 1 }] : []
      } } } });
    }
    if (query.includes("acceptProcessing")) {
      acceptCalls += 1;
      if (scenario.delayAccept) await new Promise((resolve) => setTimeout(resolve, 150));
      if (scenario.expireOnAccept) {
        status = "EXPIRED";
        return route.fulfill({ json: { data: { acceptProcessing: { status, userErrors: [{ code: "INVALID_STATE", field: null, message: "Invitation expired" }], clientMutationId: "c" } } } });
      }
      return route.fulfill({ json: { data: { acceptProcessing: { status: "ACCEPTED", userErrors: [], clientMutationId: "c" } } } });
    }
    if (query.includes("revokeInvitation")) { status = "REVOKED"; return route.fulfill({ json: { data: { revokeInvitation: { status, userErrors: [], clientMutationId: "c" } } } }); }
    if (query.includes("declareCaptureImpossibility")) {
      const variables = JSON.parse(route.request().postData() ?? "{}") as { variables?: { input?: { reason?: string } } };
      impossibleReason = variables.variables?.input?.reason ?? "";
      return route.fulfill({ json: { data: { declareCaptureImpossibility: { answer: { requirementKey: "front", mediaIds: [], impossibilityReason: impossibleReason, version: 1 }, userErrors: [], clientMutationId: "c" } } } });
    }
    if (query.includes("declareSensitiveDetectionFalsePositive")) return route.fulfill({ json: { data: { declareSensitiveDetectionFalsePositive: { media: { id: "screened-media", status: "PROCESSING" }, userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("createMediaUpload")) return route.fulfill({ json: { data: { createMediaUpload: { upload: { mediaId: "gallery-media", uploadId: "gallery-upload", expiresAt: "2099-01-01T00:00:00Z", partSizeBytes: 5 * 1024 * 1024 }, userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("presignMediaParts")) return route.fulfill({ json: { data: { presignMediaParts: { parts: [{ partNumber: 1, url: "/upload-part", expiresAt: "2099-01-01T00:00:00Z" }], userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("completeMediaUpload")) return route.fulfill({ json: { data: { completeMediaUpload: { media: { id: "gallery-media", status: scenario.rejectedUpload ? "REJECTED" : "READY" }, userErrors: [], clientMutationId: "c" } } } });
    if (query.includes("saveCaptureMetadata")) {
      const variables = JSON.parse(route.request().postData() ?? "{}") as { variables?: { input?: { captureSource?: string; replacesMediaId?: string | null } } };
      metadataSource = variables.variables?.input?.captureSource;
      replacedMediaId = variables.variables?.input?.replacesMediaId;
      return route.fulfill({ json: { data: { saveCaptureMetadata: { media: { id: "gallery-media", status: scenario.rejectedUpload ? "REJECTED" : "READY", replacesMediaId: scenario.requiredAnswer ? "ready-media" : null }, userErrors: [], clientMutationId: "c" } } } });
    }
    if (query.includes("submitCapture")) {
      submitCalls += 1;
      if (scenario.delaySubmit) await new Promise((resolve) => setTimeout(resolve, 150));
      if (scenario.lostSubmitResponse) {
        status = "COMPLETED";
        return route.fulfill({ json: { errors: [{ message: "response lost after commit", extensions: { code: "INTERNAL" } }] } });
      }
      if (scenario.submitStatus === "EXPIRED") {
        status = "EXPIRED";
        return route.fulfill({ json: { errors: [{ message: "invitation expired", extensions: { code: "INVALID_STATE" } }] } });
      }
      status = "COMPLETED";
      return route.fulfill({ json: { data: { submitCapture: { status, userErrors: [], clientMutationId: "c" } } } });
    }
    return route.fulfill({ json: { data: {} } });
  });
  return { get acceptCalls() { return acceptCalls; }, get verifyCalls() { return verifyCalls; }, get bootstrapCalls() { return bootstrapCalls; }, get submitCalls() { return submitCalls; }, get metadataSource() { return metadataSource; }, get replacedMediaId() { return replacedMediaId; } };
}

async function seedDraft(page: Page, draftId = "saved-front", pendingUpload = false) {
  await page.evaluate(async ({ draftId, pendingUpload }) => {
    const request = indexedDB.open("inspection-capture-v3", 1);
    const db = await new Promise<IDBDatabase>((resolve, reject) => {
      request.onupgradeneeded = () => {
        request.result.createObjectStore("drafts", { keyPath: "id" }).createIndex("responsibilityId", "responsibilityId", { unique: false });
        request.result.createObjectStore("quarantine", { autoIncrement: true });
      };
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
    await new Promise<void>((resolve, reject) => {
      const transaction = db.transaction("drafts", "readwrite");
      transaction.objectStore("drafts").put({
        id: draftId, responsibilityId: "responsibility-migration", schemaVersion: 1,
        blob: new ArrayBuffer(8), blobType: "image/png", sha256: "a".repeat(64), uploadId: pendingUpload ? "saved-upload" : undefined,
        mediaId: pendingUpload ? "saved-media" : undefined, metadataSaved: false,
        parts: pendingUpload ? [{ number: 1, complete: false }] : [],
        metadata: { requirementKey: "front", description: "", source: "camera", capturedAt: "2026-01-01T00:00:00.000Z" }
      });
      transaction.oncomplete = () => resolve();
      transaction.onerror = () => reject(transaction.error);
    });
    db.close();
  }, { draftId, pendingUpload });
}

async function enterOtp(page: Page, link = "/capture/migration-test") {
  const otp = page.waitForResponse((response) => response.url().includes("graphql") && (response.request().postData() ?? "").includes("requestInvitationOtp"));
  await page.goto(link);
  await otp;
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
}

async function enterConsent(page: Page, link = "/capture/migration-test") {
  await enterOtp(page, link);
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await expect(page.getByRole("heading", { name: "Uso dos seus dados" })).toBeVisible();
}

async function acceptPurposes(page: Page) {
  await page.getByLabel("Processamento das fotos").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
}

test("IT-136 invalid OTP returns a safe correction without unlocking capture", async ({ page }) => {
  const calls = await mockCapture(page, { invalidOtp: true });
  await enterOtp(page);
  await page.getByLabel("Código de seis dígitos").fill("000000");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await expect(page.getByText("Código inválido ou expirado. Confira o código recebido.")).toBeVisible();
  await expect(page.getByRole("heading", { name: "Uso dos seus dados" })).toHaveCount(0);
  expect(calls.verifyCalls).toBe(1);
});

test("IT-137 presents the OTP retry restriction and holds concurrent requests", async ({ page }) => {
  const calls = await mockCapture(page, { rateLimitedOtp: true });
  await page.goto("/capture/migration-test");
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
  await expect(page.getByText("O limite de solicitações foi atingido. Aguarde antes de pedir outro código.")).toBeVisible();
  const retry = page.getByRole("button", { name: "Solicitar outro código" });
  await retry.click();
  await expect(page.getByText("O limite de solicitações foi atingido. Aguarde antes de pedir outro código.")).toBeVisible();
  expect(calls.verifyCalls).toBe(0);
});

test("IT-138 a revoked invitation never exposes editable capture after OTP", async ({ page }) => {
  await mockCapture(page, { bootstrapStatus: "REVOKED" });
  await enterOtp(page);
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await expect(page.getByRole("heading", { name: "Acesso indisponível" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Aceitar e continuar" })).toHaveCount(0);
});

test("IT-139 repeated OTP verification submits once while pending", async ({ page }) => {
  const calls = await mockCapture(page, { delayVerify: true });
  await enterOtp(page);
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).dblclick();
  await expect(page.getByRole("heading", { name: "Uso dos seus dados" })).toBeVisible();
  expect(calls.verifyCalls).toBe(1);
});

test("IT-140 a retained responsibility draft remains behind OTP after reload without CSRF proof", async ({ page }) => {
  const calls = await mockCapture(page);
  await enterConsent(page);
  await seedDraft(page);
  const bootstrapCount = calls.bootstrapCalls;
  await page.reload();
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
  expect(calls.bootstrapCalls).toBe(bootstrapCount);
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await expect(page.getByRole("heading", { name: "Uso dos seus dados" })).toBeVisible();
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByText(/Rascunho salvo neste dispositivo; aguardando envio/)).toBeVisible();
});

test("IT-156 a saved draft resumes after a transient OTP network failure and reauthentication", async ({ page }) => {
  await mockCapture(page);
  await enterConsent(page);
  await seedDraft(page);
  const offlineGraphql = (route: import("@playwright/test").Route) => route.abort("internetdisconnected");
  await page.route("**/*graphql*", offlineGraphql);
  await page.reload();
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
  await expect(page.getByText(/Não foi possível conectar/i)).toBeVisible();
  await page.unroute("**/*graphql*", offlineGraphql);
  await page.getByRole("button", { name: "Solicitar outro código" }).click();
  await expect(page.getByText("Enviamos um novo código. Confira sua caixa de entrada.")).toBeVisible();
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await expect(page.getByRole("heading", { name: "Uso dos seus dados" })).toBeVisible();
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByText(/Rascunho salvo neste dispositivo; aguardando envio/)).toBeVisible();
});

test("IT-141 requires every separate purpose before consent advances", async ({ page }) => {
  const calls = await mockCapture(page);
  await enterConsent(page);
  await page.getByLabel("Processamento das fotos").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByText("Confirme todas as opções da divulgação para continuar.")).toBeVisible();
  expect(calls.acceptCalls).toBe(0);
  await expect(page.getByRole("heading", { name: "Uso dos seus dados" })).toBeVisible();
});

test("IT-143 an invitation that expires during consent stays unavailable", async ({ page }) => {
  const calls = await mockCapture(page, { expireOnAccept: true });
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByRole("heading", { name: "Acesso indisponível" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Tirar foto" })).toHaveCount(0);
  expect(calls.acceptCalls).toBe(1);
});

test("IT-145 does not expose review before the required consent", async ({ page }) => {
  await mockCapture(page);
  await enterConsent(page);
  await expect(page.getByRole("button", { name: "Revisar vistoria" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Aceitar e continuar" })).toBeVisible();
});

test("IT-146 unsupported files and blank impossibility reasons do not complete a requirement", async ({ page }) => {
  await mockCapture(page);
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.locator(".capture-camera-action input[type=file]").setInputFiles({ name: "not-an-image.txt", mimeType: "text/plain", buffer: Buffer.from("text") });
  await expect(page.getByText("Use JPEG, PNG, WebP, HEIC ou HEIF de até 20 MB.")).toBeVisible();
  await page.getByRole("button", { name: "Não consegue fotografar?" }).click();
  await page.getByRole("button", { name: "Registrar impossibilidade" }).click();
  await expect(page.getByText("Explique por que esta evidência não pode ser registrada.")).toBeVisible();
  await expect(page.getByText("Justificativa registrada.")).toHaveCount(0);
});

test("IT-147 reaching the existing media maximum explains the limit", async ({ page }) => {
  await mockCapture(page, { requiredAnswer: true });
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByText("1–1 foto(s) · GPS conforme a política")).toBeVisible();
  await expect(page.getByLabel("Resumo da captura")).toHaveText("1/1");
});

test("IT-149 replacing existing media keeps one active replacement while another action is attempted", async ({ page }) => {
  const calls = await mockCapture(page, { requiredAnswer: true });
  await page.route("**/upload-part", async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 200));
    await route.fulfill({ status: 200, headers: { etag: "replacement-etag" } });
  });
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.locator(".capture-camera-action input[type=file]").setInputFiles({ name: "replacement.png", mimeType: "image/png", buffer: Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/nwAAAABJRU5ErkJggg==", "base64") });
  await page.getByRole("button", { name: "Usar esta foto" }).click();
  const review = page.getByRole("button", { name: "Revisar vistoria" });
  await expect(review).toBeDisabled();
  await expect(page.getByText("Foto salva. Continue com a próxima referência.")).toBeVisible();
  await review.click();
  await expect(page.getByRole("heading", { name: "Revise antes de enviar" })).toBeVisible();
  await expect(page.locator(".capture-review div").filter({ hasText: "Evidências prontas" }).locator("dd")).toHaveText("1");
  expect(calls.replacedMediaId).toBe("ready-media");
});

test("IT-150 camera denial leaves only policy-permitted recovery options", async ({ page }) => {
  await mockCapture(page, { captureSourcePolicy: "CAMERA_ONLY" });
  await page.addInitScript(() => {
    const query = navigator.permissions.query.bind(navigator.permissions);
    Object.defineProperty(navigator, "permissions", { configurable: true, value: { query: async (descriptor: PermissionDescriptor) => descriptor.name === "camera" ? { state: "denied", onchange: null } : query(descriptor) } });
  });
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByRole("button", { name: "Tirar foto" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Escolher da galeria" })).toHaveCount(0);
  await page.getByRole("button", { name: "Não consegue fotografar?" }).click();
  await expect(page.getByLabel("Justificativa da impossibilidade")).toBeVisible();
  await expect(page.getByRole("button", { name: "Registrar impossibilidade" })).toBeVisible();
});

test("IT-151 denied required location never persists the camera preview", async ({ page }) => {
  await mockCapture(page, { gpsRequired: true });
  await page.context().grantPermissions([]);
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.locator(".capture-camera-action input[type=file]").setInputFiles({ name: "fachada.png", mimeType: "image/png", buffer: Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/nwAAAABJRU5ErkJggg==", "base64") });
  await page.getByRole("button", { name: "Usar esta foto" }).click();
  await expect(page.getByText(/A localização é obrigatória para este requisito/)).toBeVisible({ timeout: 8000 });
  await expect(page.getByText("Foto salva neste dispositivo. Envio pendente até voltar a conexão.")).toHaveCount(0);
});

test("IT-152 storage quota rejection never reports the preview as saved", async ({ page }) => {
  await page.addInitScript(() => Object.defineProperty(navigator, "storage", { configurable: true, value: { estimate: async () => ({ usage: 90, quota: 100 }) } }));
  await mockCapture(page);
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.locator(".capture-camera-action input[type=file]").setInputFiles({ name: "fachada.png", mimeType: "image/png", buffer: Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/nwAAAABJRU5ErkJggg==", "base64") });
  await page.getByRole("button", { name: "Usar esta foto" }).click();
  await expect(page.getByText(/prévia ainda não foi salva/i)).toBeVisible();
  await expect(page.getByText("Foto salva neste dispositivo. Envio pendente até voltar a conexão.")).toHaveCount(0);
});

test("IT-154 rejected media remains blocked instead of counting as ready evidence", async ({ page }) => {
  await mockCapture(page, { rejectedUpload: true });
  await page.route("**/upload-part", async (route) => route.fulfill({ status: 200, headers: { etag: "rejected-etag" } }));
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.locator(".capture-camera-action input[type=file]").setInputFiles({ name: "fachada.png", mimeType: "image/png", buffer: Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/nwAAAABJRU5ErkJggg==", "base64") });
  await page.getByRole("button", { name: "Usar esta foto" }).click();
  await expect(page.getByText(/Imagem recusada/)).toBeVisible();
  await expect(page.getByLabel("Resumo da captura")).toHaveText("0/1");
});

test("IT-158 review identifies a pending upload as a finalization block", async ({ page }) => {
  await mockCapture(page);
  await enterConsent(page);
  await seedDraft(page, "pending-front", true);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.getByRole("button", { name: "Revisar vistoria" }).click();
  await expect(page.getByText("Aguarde 1 envio(s) pendente(s) antes de confirmar.")).toBeVisible();
  await expect(page.getByText("Envios pendentes").locator("..") .getByText("1")).toBeVisible();
});

test("E2E-028 sanitizes the invitation URL and requires OTP again after reload", async ({ page }) => {
  await mockCapture(page);
  await enterConsent(page, "/capture/private-invitation-token");
  await expect(page).toHaveURL(/\/capture\/acesso$/);
  await page.reload();
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
});

test("E2E-029 reaches either confirmation-only completion or ordinary capture after explicit consent", async ({ page }) => {
  await mockCapture(page, { confirmationOnly: true });
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByRole("heading", { name: "Recebemos sua confirmação" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Adicione as evidências" })).toHaveCount(0);
});

test("E2E-029 sends an ordinary invitation to the evidence stage", async ({ page }) => {
  await mockCapture(page);
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByRole("heading", { name: "Adicione as evidências" })).toBeVisible();
});

test("E2E-049 refusal revokes the invitation and leaves capture unavailable", async ({ page }) => {
  await mockCapture(page);
  await enterConsent(page);
  await page.getByRole("button", { name: "Não aceito" }).click();
  await expect(page.getByRole("heading", { name: "Acesso indisponível" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Aceitar e continuar" })).toHaveCount(0);
});

test("IT-148 offers gallery only when both capture policies allow it", async ({ page }) => {
  await mockCapture(page, { allowGallery: true, captureSourcePolicy: "CAMERA_ONLY" });
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByRole("button", { name: "Tirar foto" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Escolher da galeria" })).toHaveCount(0);
});

test("IT-148 gallery selection uses its permitted metadata source", async ({ page }) => {
  const calls = await mockCapture(page, { allowGallery: true });
  await page.route("**/upload-part", async (route) => route.fulfill({ status: 200, headers: { etag: "gallery-etag" } }));
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.getByRole("button", { name: "Escolher da galeria" }).click();
  await page.getByLabel("Escolher uma foto da galeria").setInputFiles({
    name: "fachada.png",
    mimeType: "image/png",
    buffer: Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScL/nwAAAABJRU5ErkJggg==", "base64")
  });
  await expect(page.getByRole("button", { name: "Usar esta foto" })).toBeVisible();
  await page.getByRole("button", { name: "Usar esta foto" }).click();
  await expect(page.getByText("Recebida e verificada pelo servidor")).toBeVisible();
  expect(calls.metadataSource).toBe("GALLERY");
});

test("IT-157; E2E-032; E2E-063 require the server's accepted response before finalization", async ({ page }) => {
  const calls = await mockCapture(page, { requiredAnswer: true });
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.getByRole("button", { name: "Revisar vistoria" }).click();
  await page.getByRole("button", { name: "Enviar vistoria completa" }).click();
  await expect(page.getByRole("heading", { name: "Recebemos sua confirmação" })).toBeVisible();
  expect(calls.submitCalls).toBe(1);
});

test("E2E-063 requires explicit incomplete confirmation before the server accepts submission", async ({ page }) => {
  const calls = await mockCapture(page);
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.getByRole("button", { name: "Revisar vistoria" }).click();
  await page.getByRole("button", { name: "Enviar vistoria completa" }).click();
  await expect(page.getByText(/Ainda não há evidências válidas para todos os requisitos/i).first()).toBeVisible();
  expect(calls.submitCalls).toBe(0);
  await page.getByRole("button", { name: "Confirmar envio incompleto" }).click();
  await expect(page.getByRole("heading", { name: "Recebemos sua confirmação" })).toBeVisible();
  expect(calls.submitCalls).toBe(1);
});

test("IT-160 repeated final submission while pending sends only one request", async ({ page }) => {
  const calls = await mockCapture(page, { requiredAnswer: true, delaySubmit: true });
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.getByRole("button", { name: "Revisar vistoria" }).click();
  await page.getByRole("button", { name: "Enviar vistoria completa" }).dblclick();
  await expect(page.getByRole("heading", { name: "Recebemos sua confirmação" })).toBeVisible();
  expect(calls.submitCalls).toBe(1);
});

test("IT-161 reconciles a lost submission response before evidence cleanup", async ({ page }) => {
  const calls = await mockCapture(page, { requiredAnswer: true, lostSubmitResponse: true });
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.getByRole("button", { name: "Revisar vistoria" }).click();
  await page.getByRole("button", { name: "Enviar vistoria completa" }).click();
  await expect(page.getByRole("heading", { name: "Recebemos sua confirmação" })).toBeVisible();
  expect(calls.submitCalls).toBe(1);
});

test("IT-159; E2E-041 terminal invitation expiry precedes local readiness at submit", async ({ page }) => {
  const calls = await mockCapture(page, { requiredAnswer: true, submitStatus: "EXPIRED" });
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.getByRole("button", { name: "Revisar vistoria" }).click();
  await page.getByRole("button", { name: "Enviar vistoria completa" }).click();
  await expect(page.getByRole("heading", { name: "Acesso indisponível" })).toBeVisible();
  await expect(page.getByText(/fotos salvas neste dispositivo serão preservadas/i)).toBeVisible();
  expect(calls.submitCalls).toBe(1);
});

test("IT-142; IT-144; E2E-041 exposes all consent choices at 320 pixels and ignores a repeated activation", async ({ page }) => {
  const calls = await mockCapture(page, { delayAccept: true });
  await page.setViewportSize({ width: 320, height: 780 });
  await enterConsent(page);
  await expect(page.getByLabel("Processamento das fotos")).toBeVisible();
  await expect(page.getByLabel("Análise por inteligência artificial")).toBeVisible();
  await expect(page.getByLabel("Localização quando necessária")).toBeVisible();
  await acceptPurposes(page);
  const accept = page.getByRole("button", { name: "Aceitar e continuar" });
  await accept.dblclick();
  await expect(page.getByRole("heading", { name: "Adicione as evidências" })).toBeVisible();
  expect(calls.acceptCalls).toBe(1);
});

test("E2E-052 retains the server-confirmed impossibility reason after reauthentication", async ({ page }) => {
  await mockCapture(page);
  await enterConsent(page);
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.getByRole("button", { name: "Não consegue fotografar?" }).click();
  await page.getByLabel("Justificativa da impossibilidade").fill("Acesso bloqueado");
  await page.getByRole("button", { name: "Registrar impossibilidade" }).click();
  await expect(page.getByText("Justificativa registrada.")).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByText("Justificativa registrada: Acesso bloqueado")).toBeVisible();
});

test("E2E-053 submits a false-positive explanation without treating screening as accepted", async ({ page }) => {
  await mockCapture(page, { screenedMedia: true });
  await enterConsent(page);
  await page.evaluate(async () => {
    const request = indexedDB.open("inspection-capture-v3", 1);
    const db = await new Promise<IDBDatabase>((resolve, reject) => {
      request.onupgradeneeded = () => {
        const store = request.result.createObjectStore("drafts", { keyPath: "id" });
        store.createIndex("responsibilityId", "responsibilityId", { unique: false });
        request.result.createObjectStore("quarantine", { autoIncrement: true });
      };
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
    await new Promise<void>((resolve, reject) => {
      const transaction = db.transaction("drafts", "readwrite");
      transaction.objectStore("drafts").put({ id: "screened-draft", responsibilityId: "responsibility-migration", schemaVersion: 1, blob: new ArrayBuffer(5), blobType: "image/png", sha256: "a".repeat(64), mediaId: "screened-media", uploadId: "upload", metadataSaved: true, mediaStatus: "SCREENED", parts: [{ number: 1, etag: "etag", complete: true }], metadata: { requirementKey: "front", description: "Fachada", source: "camera", capturedAt: "2026-01-01T00:00:00.000Z" } });
      transaction.oncomplete = () => resolve();
      transaction.onerror = () => reject(transaction.error);
    });
    db.close();
  });
  await acceptPurposes(page);
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await page.getByRole("button", { name: "Declarar falso positivo" }).click();
  await page.getByLabel("Motivo do falso positivo").fill("A superfície não contém conteúdo sensível.");
  await page.getByRole("button", { name: "Enviar declaração de falso positivo" }).click();
  await expect(page.getByText("Falso positivo registrado para revisão.")).toBeVisible();
  await expect(page.getByText("Recebida; aguardando verificação do servidor")).toBeVisible();
  await expect(page.getByLabel("Resumo da captura")).toHaveText("0/1");
});
