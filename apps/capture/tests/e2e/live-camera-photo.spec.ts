import { expect, test, type Page } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { installFakeGetUserMedia } from "./support/get-user-media";

test.use({ serviceWorkers: "block" });

const req = (key: string, name: string) => ({ key, section: name, label: name, instructions: name, required: true, minimumMedia: 1, maximumMedia: 5, descriptionRequired: false, captureSourcePolicy: "CAMERA_DEFAULT", comparisonTarget: "CHECKLIST_ONLY", impossibilityAllowed: true, environmentIdentityId: `id-${key}`, environmentTypeCode: "OTHER", environmentName: name, expected: true, addedDuringCapture: false });
const env = (key: string, name: string) => ({ requirementKey: key, identityId: `id-${key}`, typeCode: "OTHER", name, expected: true, addedDuringCapture: false, mediaCount: 0, status: "PENDING" });
const bootstrap = (overrides: Record<string, unknown> = {}) => ({ responsibilityId: "live-photo-responsibility", recaptureRequestId: null, status: "OPEN", confirmationOnly: false, kind: "INSPECTION", mediaKind: "PHOTO", disclosureVersion: "v1", captureLayout: "ENVIRONMENTS", reference: { comparisonMode: "CHECKLIST_ONLY" }, referenceItems: [], policy: { allowGallery: true, gpsRequired: false }, requirements: [req("env:sala", "Sala"), req("env:cozinha", "Cozinha")], environments: [env("env:sala", "Sala"), env("env:cozinha", "Cozinha")], reviewHandoff: null, answers: [], ...overrides });

async function mockCapture(page: Page, external: ReturnType<typeof bootstrap>) {
  let sequence = 0;
  await page.route("**/photo-part", (route) => route.fulfill({ status: 200, headers: { etag: `photo-${sequence}` } }));
  await page.route("**/graphql", async (route) => {
    const { query = "", variables = {} } = JSON.parse(route.request().postData() ?? "{}") as { query?: string; variables?: { input?: Record<string, unknown> } };
    const respond = (data: unknown) => route.fulfill({ json: { data } });
    if (query.includes("requestInvitationOtp")) return respond({ requestInvitationOtp: { status: "SENT", userErrors: [] } });
    if (query.includes("verifyInvitationOtp")) return respond({ verifyInvitationOtp: { status: "VERIFIED", csrfToken: "csrf", expiresAt: "2099-01-01T00:00:00Z", userErrors: [] } });
    if (query.includes("externalCapture")) return respond({ externalCapture: external });
    if (query.includes("acceptProcessing")) return respond({ acceptProcessing: { status: "ACCEPTED", userErrors: [] } });
    if (query.includes("createMediaUpload")) { sequence += 1; return respond({ createMediaUpload: { upload: { mediaId: `photo-${sequence}`, uploadId: `upload-${sequence}`, expiresAt: "2099-01-01T00:00:00Z", partSizeBytes: 1048576 }, userErrors: [] } }); }
    if (query.includes("presignMediaParts")) return respond({ presignMediaParts: { parts: [{ partNumber: 1, url: new URL("/photo-part", page.url()).href, expiresAt: "2099-01-01T00:00:00Z" }], userErrors: [] } });
    if (query.includes("completeMediaUpload")) return respond({ completeMediaUpload: { media: { id: `photo-${sequence}`, status: "VERIFIED" }, userErrors: [] } });
    if (query.includes("saveCaptureMetadata")) {
      const key = String(variables.input?.requirementKey); const mediaId = String(variables.input?.mediaId ?? `photo-${sequence}`);
      const answers = external.answers as Array<Record<string, unknown>>;
      (external as Record<string, unknown>).answers = [...answers.filter((answer) => answer.requirementKey !== key), { requirementKey: key, mediaIds: [...new Set([...(answers.find((answer) => answer.requirementKey === key)?.mediaIds as string[] ?? []), mediaId])], impossibilityReason: null, version: 1 }];
      return respond({ saveCaptureMetadata: { media: { id: mediaId, status: "READY", replacesMediaId: null }, userErrors: [] } });
    }
    throw new Error(`Operação GraphQL inesperada: ${query.slice(0, 80)}`);
  });
}

async function authenticate(page: Page, external = bootstrap()) {
  await mockCapture(page, external);
  await page.goto("/capture/foto-ao-vivo");
  await page.getByLabel("Código de seis dígitos").fill("123456");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await page.getByLabel("Processamento das fotos").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
}

test("câmera ao vivo fotografa um ambiente e avança ao próximo pendente", async ({ page }) => {
  await installFakeGetUserMedia(page, { kind: "canvas", width: 960, height: 1280 });
  await page.setViewportSize({ width: 390, height: 844 });
  await authenticate(page);
  await page.getByRole("button", { name: "Abrir câmera" }).click();
  const dialog = page.getByRole("dialog", { name: "Câmera · Sala" });
  await expect(dialog).toBeVisible();
  const accessibility = await new AxeBuilder({ page: page as never }).include(".camera-stage").analyze();
  expect(accessibility.violations.filter((violation) => ["serious", "critical"].includes(violation.impact ?? ""))).toEqual([]);
  const shutter = dialog.getByRole("button", { name: "Fotografar" });
  await expect(shutter).toBeEnabled();
  await shutter.click();
  await expect(dialog.getByRole("img", { name: "Prévia da foto capturada" })).toBeVisible();
  await expect(dialog.locator(".camera-stage__overlay-top")).toHaveCount(0);
  await expect(dialog.locator(".camera-stage__thumbnail")).not.toContainText("0");
  await dialog.getByRole("button", { name: "Usar esta foto" }).click();
  const activeDialog = page.getByRole("dialog", { name: /^Câmera · / });
  await expect(activeDialog.getByRole("status").filter({ hasText: "Foto salva" })).toBeVisible();
  await expect(activeDialog.getByRole("button", { name: "Sala, concluído" })).toBeVisible();
  await expect(page.getByRole("dialog", { name: "Câmera · Cozinha" })).toBeVisible();
  await expect(activeDialog.locator(".camera-stage__heading")).toHaveCSS("color", "rgb(255, 255, 255)");
  const layout = await activeDialog.evaluate((element) => {
    const box = (selector: string) => {
      const rect = element.querySelector<HTMLElement>(selector)!.getBoundingClientRect();
      return { top: rect.top, bottom: rect.bottom };
    };
    return [".camera-stage__topbar", ".camera-stage__frame", ".camera-stage__strip", ".camera-stage__controls"].map(box);
  });
  expect(layout[0].bottom).toBeLessThanOrEqual(layout[1].top);
  expect(layout[1].bottom).toBeLessThanOrEqual(layout[2].top);
  expect(layout[2].bottom).toBeLessThanOrEqual(layout[3].top);
});

for (const allowGallery of [true, false]) {
  test(`câmera bloqueada oferece galeria conforme a política (${allowGallery ? "permitida" : "bloqueada"})`, async ({ page }) => {
    await installFakeGetUserMedia(page, { kind: "permission-denied" });
    await authenticate(page, bootstrap({ policy: { allowGallery, gpsRequired: false } }));
    await page.getByRole("button", { name: "Abrir câmera" }).click();
    const dialog = page.getByRole("dialog", { name: "Câmera · Sala" });
    const sheet = dialog.getByRole("region", { name: "A câmera está bloqueada" });
    await expect(sheet).toBeVisible();
    await expect(sheet.getByRole("button", { name: "Usar câmera do aparelho" })).toBeVisible();
    await expect(sheet.getByRole("button", { name: "Escolher da galeria" })).toHaveCount(allowGallery ? 1 : 0);
  });
}

test("câmera em uso mostra orientação em português", async ({ page }) => {
  await installFakeGetUserMedia(page, { kind: "not-readable" });
  await authenticate(page);
  await page.getByRole("button", { name: "Abrir câmera" }).click();
  await expect(page.getByRole("region", { name: "A câmera está em uso" })).toBeVisible();
});

test("visor mantém proporção vertical e fecha com Escape liberando a câmera", async ({ page }) => {
  await installFakeGetUserMedia(page, { kind: "canvas", width: 960, height: 1280, captureConstraints: true });
  await page.setViewportSize({ width: 320, height: 568 });
  await authenticate(page);
  const originalThemeColor = await page.locator('meta[name="theme-color"]').getAttribute("content");
  const opener = page.getByRole("button", { name: "Abrir câmera" });
  await opener.click();
  await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute("content", "#0b1118");
  const dialog = page.getByRole("dialog", { name: "Câmera · Sala" });
  const shutter = dialog.getByRole("button", { name: "Fotografar" });
  await expect(shutter).toBeEnabled();
  const geometry = await page.evaluate(() => {
    const view = document.querySelector<HTMLElement>(".camera-stage__viewfinder")!.getBoundingClientRect();
    const button = document.querySelector<HTMLElement>('.camera-stage button[aria-label="Fotografar"]')!.getBoundingClientRect();
    return { ratio: view.width / view.height, buttonRight: button.right, width: document.documentElement.scrollWidth };
  });
  expect(geometry.width).toBeLessThanOrEqual(320);
  expect(geometry.buttonRight).toBeLessThanOrEqual(320);
  expect(geometry.ratio).toBeCloseTo(0.75, 2);
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute("content", originalThemeColor ?? "");
  await expect(opener).toBeFocused();
  await expect.poll(() => page.evaluate(() => (window as typeof window & { cameraTrackStops?: number }).cameraTrackStops ?? 0)).toBeGreaterThan(0);
});

test("paisagem mantém todos os ambientes visíveis abaixo do visor", async ({ page }) => {
  await installFakeGetUserMedia(page, { kind: "canvas", width: 960, height: 1280 });
  await page.setViewportSize({ width: 844, height: 390 });
  await authenticate(page);
  await page.getByRole("button", { name: "Abrir câmera" }).click();
  const dialog = page.getByRole("dialog", { name: "Câmera · Sala" });
  const shutter = dialog.getByRole("button", { name: "Fotografar" });
  await expect(shutter).toBeVisible();
  const geometry = await dialog.evaluate((element) => {
    const bounds = (selector: string) => {
      const rect = element.querySelector<HTMLElement>(selector)!.getBoundingClientRect();
      return { x: rect.x, y: rect.y, right: rect.right, bottom: rect.bottom, width: rect.width, height: rect.height };
    };
    return {
      viewport: { width: document.documentElement.clientWidth, height: document.documentElement.clientHeight, scrollWidth: document.documentElement.scrollWidth },
      shutter: bounds('.camera-stage button[aria-label="Fotografar"]'),
      frame: bounds(".camera-stage__viewfinder"),
      strip: bounds(".camera-stage__strip"),
      rail: bounds(".camera-stage__topbar"),
      chips: [...element.querySelectorAll<HTMLElement>(".camera-stage__chip")].map((chip) => {
        const rect = chip.getBoundingClientRect();
        return { x: rect.x, y: rect.y, right: rect.right, bottom: rect.bottom };
      }),
    };
  });
  expect(geometry.shutter.x).toBeGreaterThanOrEqual(0);
  expect(geometry.shutter.y).toBeGreaterThanOrEqual(0);
  expect(geometry.shutter.right).toBeLessThanOrEqual(geometry.viewport.width);
  expect(geometry.shutter.bottom).toBeLessThanOrEqual(geometry.viewport.height);
  expect(geometry.strip.y).toBeGreaterThanOrEqual(geometry.frame.bottom - 1);
  expect(geometry.strip.right).toBeLessThanOrEqual(geometry.rail.x + 1);
  expect(geometry.chips).toHaveLength(2);
  for (const chip of geometry.chips) {
    expect(chip.x).toBeGreaterThanOrEqual(0);
    expect(chip.y).toBeGreaterThanOrEqual(0);
    expect(chip.right).toBeLessThanOrEqual(geometry.viewport.width);
    expect(chip.bottom).toBeLessThanOrEqual(geometry.viewport.height);
  }
  expect(geometry.viewport.scrollWidth).toBeLessThanOrEqual(geometry.viewport.width);
});

test("fluxo v1 não mostra botão Abrir câmera para fotos", async ({ page }) => {
  await authenticate(page, bootstrap({ captureLayout: null, requirements: [{ ...req("photo:main", "Ambiente"), key: "photo:main" }], environments: [] }));
  await expect(page.getByRole("button", { name: "Abrir câmera" })).toHaveCount(0);
});

test("diálogo da câmera usa somente textos em português", async ({ page }) => {
  await installFakeGetUserMedia(page, { kind: "canvas", width: 960, height: 1280 });
  await authenticate(page);
  await page.getByRole("button", { name: "Abrir câmera" }).click();
  const dialog = page.getByRole("dialog", { name: "Câmera · Sala" });
  await expect(dialog).toBeVisible();
  const text = await dialog.innerText();
  expect(text).not.toMatch(/\b(camera|photo|retry|cancel|close|loading|error)\b/i);
});
