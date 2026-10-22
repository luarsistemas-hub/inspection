import { createHash } from "node:crypto";
import { readFile, stat } from "node:fs/promises";
import { resolve } from "node:path";
import { execFileSync } from "node:child_process";
import { expect, type APIRequestContext, type Page, type TestInfo } from "@playwright/test";
import { loginAsLocalAdmin } from "./auth";

export const apiURL = process.env.PLAYWRIGHT_API_URL ?? `http://localhost:${process.env.INSPECTION_API_PORT ?? "8080"}/graphql`;
export const dashboardBaseURL = process.env.PLAYWRIGHT_BASE_URL ?? "http://localhost:3002";
export const captureBaseURL = process.env.PLAYWRIGHT_CAPTURE_BASE_URL ?? "http://localhost:3003";
export const mailpitURL = process.env.PLAYWRIGHT_MAILPIT_URL ?? "http://localhost:8026";
export const fixtureDirectory = resolve(process.cwd(), "tests/fixtures/video");
export const originPath = resolve(fixtureDirectory, "origem.mp4");
export const currentPath = resolve(fixtureDirectory, "atual.mp4");
const oidcTokenURL = process.env.NEXT_PUBLIC_OIDC_TOKEN_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/token";

type GraphQLResponse<T> = { data?: T; errors?: Array<{ message: string; extensions?: { code?: string } }> };
type MailpitMessage = { ID: string; Created: string; Subject: string; To: Array<{ Address: string }> };
type MailpitList = { messages: MailpitMessage[] };
type MailpitDetail = { Text: string; HTML: string };
export type VideoScenario = {
  id: string;
  assetId: string;
  assetName: string;
  externalKey: string;
  businessUnitId: string;
  participantId: string;
  participantName: string;
  templateId: string;
  templateVersionId: string;
  segmentVersionId: string;
  email: string;
  originVersionId?: string;
  originPreparationId?: string;
  originMediaId?: string;
  inspectionId?: string;
  currentPreparationId?: string;
  currentMediaId?: string;
  reportId?: string;
};

async function localEnv(): Promise<Record<string, string>> {
  const values: Record<string, string> = {};
  try {
    const content = await readFile(resolve(process.cwd(), "../../.env.inspection"), "utf8");
    for (const line of content.split(/\r?\n/)) {
      const match = line.match(/^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*?)\s*$/);
      if (match) values[match[1]] = match[2].replace(/^(?:"([\s\S]*)"|'([\s\S]*)')$/, (_whole, double: string, single: string) => double ?? single);
    }
  } catch { /* The preflight reports the missing local environment file. */ }
  const runtimeValues = Object.fromEntries(Object.entries(process.env).filter((entry): entry is [string, string] => typeof entry[1] === "string"));
  return { ...values, ...runtimeValues };
}

async function reachable(url: string): Promise<void> {
  const response = await fetch(url, { signal: AbortSignal.timeout(5_000) });
  if (!response.ok) throw new Error(`${url} respondeu HTTP ${response.status}`);
}

export async function preflight(requiresOrigin: boolean): Promise<void> {
  for (const name of [...(requiresOrigin ? ["origem.mp4"] : []), "atual.mp4"]) {
    const path = resolve(fixtureDirectory, name);
    let file;
    try { file = await stat(path); } catch { throw new Error(`Vídeo ausente: ${path}. Consulte ${resolve(fixtureDirectory, "README.md")} para prepará-lo.`); }
    if (!file.isFile() || file.size === 0 || file.size > 1024 ** 3) throw new Error(`${path} precisa ser um MP4 não vazio de até 1 GiB.`);
  }
  const env = await localEnv();
  for (const name of ["LLM_API_KEY", "INSPECTION_LITELLM_API_KEY", "LITELLM_MASTER_KEY"]) {
    if (!env[name]) throw new Error(`A configuração live local não define ${name}. Revise .env.inspection.`);
  }
  for (const [name, url] of [
    ["API", new URL("/readyz", apiURL).toString()],
    ["Dashboard", new URL("/inspections", dashboardBaseURL).toString()],
    ["Capture", new URL("/manifest.webmanifest", captureBaseURL).toString()],
    ["Mailpit", new URL("/api/v1/messages?limit=1", mailpitURL).toString()],
  ]) {
    try { await reachable(url); } catch (error) { throw new Error(`${name} local indisponível: ${(error as Error).message}. Inicie a stack com ./scripts/local.sh up.`); }
  }
  try {
    const services = ["inspection-api", "inspection-worker", "inspection-video-worker"];
    const modes = services.map((service) => {
      const ids = execFileSync("docker", ["ps", "--filter", `label=com.docker.compose.service=${service}`, "--format", "{{.ID}}"], { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim().split(/\r?\n/).filter(Boolean);
      return ids.flatMap((id) => execFileSync("docker", ["inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", id], { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim().split(/\r?\n/).filter((line) => /^INSPECTION_LLM_MODE=/.test(line)));
    });
    const mode = modes.flat();
    if (mode.length < 3 || mode.some((line) => line !== "INSPECTION_LLM_MODE=live")) throw new Error("API, worker e worker de vídeo precisam estar com INSPECTION_LLM_MODE=live. Reinicie com INSPECTION_LLM_MODE=live ./scripts/local.sh up.");
  } catch (error) {
    if (error instanceof Error && error.message.includes("INSPECTION_LLM_MODE")) throw error;
    throw new Error("Não consegui confirmar a configuração efetiva da API e dos workers pelo Docker. Suba os serviços locais com INSPECTION_LLM_MODE=live.");
  }
}

export class LocalGraphQL {
  private authorization = "";
  private membershipId = "";
  private refreshToken = "";
  constructor(private readonly request: APIRequestContext, private readonly dashboard: Page) {
    dashboard.on("request", (outgoing) => {
      if (!outgoing.url().endsWith("/graphql")) return;
      const headers = outgoing.headers();
      if (headers.authorization?.startsWith("Bearer ")) this.authorization = headers.authorization;
      if (headers["x-inspection-membership-id"]) this.membershipId = headers["x-inspection-membership-id"];
    });
    dashboard.on("request", (outgoing) => {
      if (outgoing.url() !== oidcTokenURL || !outgoing.postData()?.includes("grant_type=refresh_token")) return;
      const body = new URLSearchParams(outgoing.postData() ?? "");
      this.refreshToken = body.get("refresh_token") ?? this.refreshToken;
    });
    dashboard.on("response", async (response) => {
      if (response.url() !== oidcTokenURL || !response.ok()) return;
      const body = await response.json().catch(() => undefined) as { access_token?: string; refresh_token?: string } | undefined;
      if (body?.access_token) this.authorization = `Bearer ${body.access_token}`;
      if (body?.refresh_token) this.refreshToken = body.refresh_token;
    });
  }

  async login(path = "/inspections"): Promise<void> {
    await loginAsLocalAdmin(this.dashboard, path);
    await expect.poll(() => this.authorization, { timeout: 15_000, message: "sessão autenticada do Dashboard" }).toMatch(/^Bearer\s+\S+/);
  }

  async execute<T>(query: string, variables: Record<string, unknown> = {}): Promise<T> {
    if (!this.authorization) throw new Error("A sessão autenticada do Dashboard ainda não foi observada.");
    for (let attempt = 0; attempt < 2; attempt++) {
      const response = await this.request.post(apiURL, {
        headers: { authorization: this.authorization, ...(this.membershipId ? { "X-Inspection-Membership-ID": this.membershipId } : {}) },
        data: { query, variables },
        timeout: 30_000,
      });
      if (response.status() === 401 && attempt === 0) {
        if (!this.refreshToken) throw new Error("Sessão local expirou e não há refresh token capturado do login do Dashboard.");
        const refreshed = await this.request.post(oidcTokenURL, {
          form: { grant_type: "refresh_token", refresh_token: this.refreshToken, client_id: "inspection-dashboard" },
          timeout: 15_000,
        });
        if (!refreshed.ok()) throw new Error(`Renovação da sessão do Dashboard falhou: HTTP ${refreshed.status()}.`);
        const tokens = await refreshed.json() as { access_token?: string; refresh_token?: string };
        if (!tokens.access_token) throw new Error("O provedor local não retornou um access token válido.");
        this.authorization = `Bearer ${tokens.access_token}`;
        this.refreshToken = tokens.refresh_token ?? this.refreshToken;
        continue;
      }
      const body = await response.json() as GraphQLResponse<T>;
      if (!response.ok() || body.errors?.length || !body.data) throw new Error(`GraphQL falhou: ${body.errors?.map((error) => error.message).join(" | ") ?? `HTTP ${response.status()}`}`);
      return body.data;
    }
    throw new Error("Não foi possível renovar a sessão do Dashboard.");
  }
}

export async function mailpitIDs(request: APIRequestContext): Promise<Set<string>> {
  const response = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
  expect(response.ok(), "Mailpit deve responder à consulta de mensagens").toBe(true);
  const result = await response.json() as MailpitList;
  return new Set(result.messages.map((message) => message.ID));
}

async function newInvitationLink(request: APIRequestContext, knownIDs: Set<string>, email: string): Promise<string> {
  let captureURL = "";
  await expect.poll(async () => {
    const response = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
    if (!response.ok()) return "";
    const { messages } = await response.json() as MailpitList;
    const latest = messages.filter((item) => !knownIDs.has(item.ID) && item.To.some((to) => to.Address === email)).sort((left, right) => Date.parse(right.Created) - Date.parse(left.Created))[0];
    if (!latest) return "";
    const detail = await request.get(`${mailpitURL}/api/v1/message/${latest.ID}`);
    if (!detail.ok()) return "";
    const body = await detail.json() as MailpitDetail;
    const text = `${body.Text}\n${body.HTML}`;
    captureURL = text.match(/https?:\/\/[^\s<>"']*localhost:[0-9]+\/[^\s<>"']+/)?.[0]?.replace(/&amp;/g, "&").replace(/[),.;]+$/, "") ?? "";
    return captureURL;
  }, { timeout: 90_000, intervals: [1_000, 2_000, 5_000], message: `convite de captura enviado para ${email}` }).toMatch(/^https?:\/\//);
  const link = new URL(captureURL);
  link.host = new URL(captureBaseURL).host;
  link.protocol = new URL(captureBaseURL).protocol;
  return link.toString();
}

async function otpCode(request: APIRequestContext, knownIDs: Set<string>, email: string): Promise<string> {
  let code = "";
  await expect.poll(async () => {
    const response = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
    if (!response.ok()) return "";
    const { messages } = await response.json() as MailpitList;
    const latest = messages.filter((item) => !knownIDs.has(item.ID) && item.Subject === "Seu código de acesso" && item.To.some((to) => to.Address === email)).sort((left, right) => Date.parse(right.Created) - Date.parse(left.Created))[0];
    if (!latest) return "";
    const detail = await request.get(`${mailpitURL}/api/v1/message/${latest.ID}`);
    if (!detail.ok()) return "";
    const body = await detail.json() as MailpitDetail;
    code = `${body.Text}\n${body.HTML}`.match(/Código:\s*(\d{6})/)?.[1] ?? "";
    return code;
  }, { timeout: 60_000, intervals: [1_000, 2_000, 5_000], message: `OTP do Capture enviado para ${email}` }).toMatch(/^\d{6}$/);
  return code;
}

async function enterCapture(page: Page, request: APIRequestContext, inviteURL: string, email: string): Promise<void> {
  const priorMessages = await mailpitIDs(request);
  await page.goto(inviteURL);
  await expect(page.getByRole("heading", { name: "Confirme seu acesso" })).toBeVisible();
  await page.getByLabel("Código de seis dígitos").fill(await otpCode(request, priorMessages, email));
  await page.getByRole("button", { name: "Confirmar código" }).click();
  await expect(page.getByLabel("Processamento do vídeo e imagens derivadas")).toBeVisible({ timeout: 20_000 });
  await page.getByLabel("Processamento do vídeo e imagens derivadas").check();
  await page.getByLabel("Análise por inteligência artificial").check();
  await page.getByLabel("Localização quando necessária").check();
  await page.getByRole("button", { name: "Aceitar e continuar" }).click();
  await expect(page.getByRole("heading", { name: /Registrar (vistoria em vídeo|vídeo de origem)/ })).toBeVisible();
}

export async function uploadAndSubmitCapture(page: Page, request: APIRequestContext, invitationSnapshot: Set<string>, email: string, filePath: string, expectedPreparation?: LocalGraphQL): Promise<{ mediaId: string; preparationId: string }> {
  const inviteURL = await newInvitationLink(request, invitationSnapshot, email);
  await enterCapture(page, request, inviteURL, email);
  const uploadCompleted = page.waitForResponse((response) => response.url().endsWith("/graphql") && response.request().postData()?.includes("completeMediaUpload") === true, { timeout: 5 * 60_000 });
  // Avoid an unhandled rejection when an earlier step fails and the page closes.
  uploadCompleted.catch(() => undefined);
  const input = page.locator('input[type="file"][accept^="video/mp4"]');
  await input.setInputFiles(filePath);
  await expect(page.getByRole("heading", { name: "Revise a gravação" })).toBeVisible({ timeout: 90_000 });
  await expect(page.getByRole("button", { name: "Confirmar e enviar" })).toBeEnabled({ timeout: 60_000 });
  await page.getByRole("button", { name: "Confirmar e enviar" }).click();
  await expect(page.getByText("Vídeo recebido", { exact: true })).toBeVisible({ timeout: 120_000 });
  const completed = await (await uploadCompleted).json().catch(() => undefined) as { data?: { completeMediaUpload?: { media?: { id?: string } } } } | undefined;
  const mediaId = completed?.data?.completeMediaUpload?.media?.id ?? "";
  expect(mediaId, "mediaId retornado pelo upload real").not.toBe("");
  await page.getByRole("button", { name: "Confirmar vistoria" }).click();
  await expect(page.getByText("Envio confirmado.", { exact: true }).first()).toBeVisible({ timeout: 30_000 });
  if (!expectedPreparation) return { mediaId, preparationId: "" };
  const preparation = await waitForPreparation(expectedPreparation, mediaId);
  return { mediaId, preparationId: preparation.preparationId };
}

export async function waitForPreparation(api: LocalGraphQL, mediaId: string): Promise<{ preparationId: string; processedFrames: number }> {
  let state = { status: "", stage: "", errorCode: "", preparationId: "", processedFrames: 0 };
  const deadline = Date.now() + 8 * 60_000;
  while (Date.now() < deadline) {
    const data = await api.execute<{ videoPreparation: { status: string; stage: string; errorCode: string; preparationId: string; processedFrames: number } }>(
      `query VideoE2EPreparation($mediaId: ID!) { videoPreparation(mediaId: $mediaId) { status stage errorCode preparationId processedFrames } }`, { mediaId });
    state = data.videoPreparation;
    if (["FAILED", "CANCELED", "REJECTED", "SCREENED", "SENSITIVE_CONTENT"].includes(state.status)) throw new Error(`Preparação do vídeo ${mediaId} bloqueada: status=${state.status}, stage=${state.stage}, code=${state.errorCode}`);
    if (state.status === "READY") break;
    await new Promise((resolve) => setTimeout(resolve, 5_000));
  }
  if (state.status !== "READY") throw new Error(`Preparação do vídeo ${mediaId} demorou mais de 8 minutos: ${JSON.stringify(state)}`);
  expect(state.processedFrames, `frames extraídos do vídeo ${mediaId}`).toBeGreaterThan(0);
  return { preparationId: state.preparationId, processedFrames: state.processedFrames };
}

export async function createVideoScenario(api: LocalGraphQL): Promise<VideoScenario> {
  const seed = await api.execute<{ businessUnits: { nodes: Array<{ id: string }> }; assets: { nodes: Array<{ businessUnitId: string; segmentVersionId: string }> } }>(
    `query VideoE2ESeed { businessUnits(first: 50) { nodes { id } } assets(first: 100) { nodes { businessUnitId segmentVersionId } } }`);
  const seedAsset = seed.assets.nodes.find((item) => item.segmentVersionId && seed.businessUnits.nodes.some((unit) => unit.id === item.businessUnitId));
  if (!seedAsset) throw new Error("Execute o seed local do Inspection antes da suíte para criar unidade e segmento de QA.");
  const unit = seed.businessUnits.nodes.find((item) => item.id === seedAsset.businessUnitId)!;
  const id = crypto.randomUUID();
  const suffix = id.slice(0, 8);
  const participantName = `Responsável E2E vídeo ${suffix}`;
  // O notifier de OTP do ambiente local entrega apenas ao endereço QA permitido.
  const email = "qa.inspection@example.test";
  const participantResult = await api.execute<{ upsertParticipant: { participant: { id: string; version: number; contacts: Array<{ id: string; channel: string; value: string }> }; userErrors: Array<{ message: string }> } }>(
    `mutation VideoE2ECreateParticipant($input: UpsertParticipantInput!) { upsertParticipant(input: $input) { participant { id version contacts { id channel value } } userErrors { message } } }`,
    { input: { businessUnitId: unit.id, name: participantName, segmentRole: "OWNER", contacts: [{ channel: "EMAIL", value: email }], clientMutationId: `video-e2e-participant-${id}` } });
  expect(participantResult.upsertParticipant.userErrors).toEqual([]);
  const participant = participantResult.upsertParticipant.participant;
  const emailContact = participant.contacts.find((contact) => contact.channel === "EMAIL" && contact.value === email);
  expect(emailContact, "contato exclusivo de email criado para o responsável").toBeTruthy();
  const verified = await api.execute<{ verifyContact: { contact: { id: string; verified: boolean } | null; userErrors: Array<{ message: string }> } }>(
    `mutation VideoE2EVerifyContact($input: VerifyContactInput!) { verifyContact(input: $input) { contact { id verified } userErrors { message } } }`,
    { input: { contactId: emailContact!.id, verified: true, clientMutationId: `video-e2e-verify-${id}` } });
  expect(verified.verifyContact.userErrors).toEqual([]);
  expect(verified.verifyContact.contact).toMatchObject({ id: emailContact!.id, verified: true });
  const channels = await api.execute<{ setDeliveryChannels: { participant: { id: string; selectedContactIds: string[] }; userErrors: Array<{ message: string }> } }>(
    `mutation VideoE2ESetContact($input: SetDeliveryChannelsInput!) { setDeliveryChannels(input: $input) { participant { id selectedContactIds } userErrors { message } } }`,
    { input: { participantId: participant.id, contactIds: [emailContact!.id], expectedVersion: participant.version, clientMutationId: `video-e2e-channels-${id}` } });
  expect(channels.setDeliveryChannels.userErrors).toEqual([]);
  expect(channels.setDeliveryChannels.participant.selectedContactIds).toContain(emailContact!.id);
  const comparison = "CHECKLIST_ONLY";
  const definition = {
    schemaVersion: 1,
    isVideo: true,
    segmentVersionId: seedAsset.segmentVersionId,
    participantRoles: ["OWNER"],
    defaultComparisonMode: comparison,
    requirements: [{ key: "video:main", section: "Vistoria", label: "Vídeo do imóvel", instructions: "Mostre os ambientes com boa iluminação e movimentos lentos.", evidenceKind: "VIDEO", minimumCount: 1, maximumCount: 1, required: true, descriptionRequired: false, captureSourcePolicy: "CAMERA_OR_GALLERY" }],
    analysisType: "REAL_ESTATE",
    policy: { gpsRequired: false, geofenceMeters: 150, allowGallery: true },
  };
  const template = await api.execute<{ publishTemplateVersion: { template: { id: string; version: number }; version: { id: string }; userErrors: Array<{ message: string }> } }>(
    `mutation VideoE2ECreateTemplate($input: PublishTemplateVersionInput!) { publishTemplateVersion(input: $input) { template { id version } version { id } userErrors { message } } }`,
    { input: { key: `e2e-video-${suffix}`, name: `E2E vídeo ${suffix}`, definition, clientMutationId: `video-e2e-template-${id}` } });
  expect(template.publishTemplateVersion.userErrors).toEqual([]);
  const activated = await api.execute<{ activateTemplateVersion: { template: { id: string; version: number }; userErrors: Array<{ message: string }> } }>(
    `mutation VideoE2EActivateTemplate($input: ActivateTemplateVersionInput!) { activateTemplateVersion(input: $input) { template { id version } userErrors { message } } }`,
    { input: { versionId: template.publishTemplateVersion.version.id, expectedVersion: template.publishTemplateVersion.template.version, clientMutationId: `video-e2e-activate-${id}` } });
  expect(activated.activateTemplateVersion.userErrors).toEqual([]);
  const assetName = `E2E vídeo ${suffix}`;
  const externalKey = `VIDEO-${suffix.toUpperCase()}`;
  const registered = await api.execute<{ registerAsset: { asset: { id: string }; userErrors: Array<{ message: string }> } }>(
    `mutation VideoE2ERegisterAsset($input: RegisterAssetInput!) { registerAsset(input: $input) { asset { id } userErrors { message } } }`,
    { input: { asset: { businessUnitId: unit.id, segmentVersionId: seedAsset.segmentVersionId, templateId: template.publishTemplateVersion.template.id, name: assetName, externalKey, address: `Imóvel de teste ${suffix}`, attributes: { propertyType: "residential" }, policyOverrides: { allowGallery: true }, assignments: [{ participantId: participant.id, role: "OWNER" }] }, clientMutationId: `video-e2e-asset-${id}` } });
  expect(registered.registerAsset.userErrors).toEqual([]);
  return { id, assetId: registered.registerAsset.asset.id, assetName, externalKey, businessUnitId: unit.id, participantId: participant.id, participantName, templateId: template.publishTemplateVersion.template.id, templateVersionId: template.publishTemplateVersion.version.id, segmentVersionId: seedAsset.segmentVersionId, email };
}

export async function createInspectionFromDashboard(page: Page, api: LocalGraphQL, scenario: VideoScenario, mode: "CHECKLIST_ONLY" | "FIXED_ORIGIN"): Promise<string> {
  await api.login("/inspections");
  await page.getByRole("button", { name: "Nova vistoria" }).click();
  const dialog = page.getByRole("dialog", { name: "Nova vistoria" });
  const selectEntity = async (label: string, name: string) => {
    const combobox = dialog.getByRole("combobox", { name: label });
    await combobox.click();
    await combobox.fill(name);
    await page.getByRole("option", { name: new RegExp(name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")) }).click();
  };
  await selectEntity("Imóvel", scenario.assetName);
  await selectEntity("Responsável pela vistoria", scenario.participantName);
  await selectEntity("Modelo de vistoria", scenario.assetName);
  await dialog.getByLabel("Comparação").selectOption(mode);
  if (mode === "FIXED_ORIGIN") {
    await expect(dialog.getByLabel("Origem")).toBeEnabled();
    await dialog.getByLabel("Origem").selectOption(scenario.originVersionId!);
  }
  const start = new Date(Date.now() + 24 * 60 * 60_000);
  const next = new Date(start.getTime() + 24 * 60 * 60_000);
  const localInput = (date: Date) => new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);
  await dialog.getByLabel("Vencimento").fill(localInput(start));
  await dialog.getByLabel("Prazo final").fill(localInput(next));
  const responsePromise = page.waitForResponse((response) => response.url().endsWith("/graphql") && response.request().postData()?.includes("mutation PlanInspection") === true);
  await dialog.getByRole("button", { name: "Planejar vistoria" }).click();
  const response = await responsePromise;
  const body = await response.json() as { data?: { planInspection?: { inspection?: { id?: string }; userErrors?: Array<{ message: string }> } }; errors?: Array<{ message: string }> };
  if (!response.ok() || body.errors?.length || body.data?.planInspection?.userErrors?.length || !body.data?.planInspection?.inspection?.id) throw new Error(`Não foi possível criar a vistoria: ${JSON.stringify(body.errors ?? body.data?.planInspection?.userErrors)}`);
  const inspectionId = body.data.planInspection.inspection.id;
  await expect(dialog).toBeHidden();
  scenario.inspectionId = inspectionId;
  const detail = await api.execute<{ inspection: { status: string; comparisonMode: string; referenceVersionId: string | null } | null }>(
    `query VideoE2EInspection($id: ID!) { inspection(id: $id) { status comparisonMode referenceVersionId } }`, { id: inspectionId });
  expect(detail.inspection).toMatchObject({ comparisonMode: mode, ...(scenario.originVersionId ? { referenceVersionId: scenario.originVersionId } : {}) });
  return inspectionId;
}

export async function createOriginInvitation(api: LocalGraphQL, scenario: VideoScenario, request: APIRequestContext): Promise<{ versionId: string; messages: Set<string> }> {
  const messages = await mailpitIDs(request);
  const data = await api.execute<{ inviteOriginCapture: { originVersionId: string | null; userErrors: Array<{ message: string }> } }>(
    `mutation VideoE2EInviteOrigin($input: InviteOriginCaptureInput!) { inviteOriginCapture(input: $input) { originVersionId userErrors { message } } }`,
    { input: { assetId: scenario.assetId, participantId: scenario.participantId, expiresAt: new Date(Date.now() + 24 * 60 * 60_000).toISOString(), clientMutationId: `video-e2e-origin-${scenario.id}` } });
  expect(data.inviteOriginCapture.userErrors).toEqual([]);
  expect(data.inviteOriginCapture.originVersionId).toBeTruthy();
  scenario.originVersionId = data.inviteOriginCapture.originVersionId!;
  return { versionId: scenario.originVersionId, messages };
}

export async function activateOrigin(api: LocalGraphQL, scenario: VideoScenario): Promise<void> {
  const result = await api.execute<{ activateOriginVersion: { version: { id: string; status: string; selectable: boolean } | null; userErrors: Array<{ message: string }> } }>(
    `mutation VideoE2EActivateOrigin($input: OriginVersionInput!) { activateOriginVersion(input: $input) { version { id status selectable } userErrors { message } } }`,
    { input: { versionId: scenario.originVersionId, clientMutationId: `video-e2e-activate-origin-${scenario.id}` } });
  expect(result.activateOriginVersion.userErrors).toEqual([]);
  expect(result.activateOriginVersion.version).toMatchObject({ id: scenario.originVersionId, status: "ACTIVE", selectable: true });
}

type ReportEvidencePage = { nodes: Array<Record<string, unknown>>; pageInfo: { endCursor: string | null; hasNextPage: boolean } };
const reportEvidenceFields = "nodes { id requirementKey role evidenceKind frameId preparationId sourceMediaId availability url timestampMs } pageInfo { endCursor hasNextPage }";

async function remainingReportEvidence(api: LocalGraphQL, inspectionId: string, first: ReportEvidencePage): Promise<Array<Record<string, unknown>>> {
  const evidence = [...first.nodes];
  let { endCursor: after, hasNextPage: hasNext } = first.pageInfo;
  while (hasNext && after) {
    const next = await api.execute<{ report: { evidence: ReportEvidencePage } | null }>(
      `query VideoE2EReportEvidence($inspectionId: ID!, $after: String) { report(inspectionId: $inspectionId) { evidence(first: 100, after: $after) { ${reportEvidenceFields} } } }`, { inspectionId, after });
    if (!next.report) break;
    evidence.push(...next.report.evidence.nodes);
    ({ endCursor: after, hasNextPage: hasNext } = next.report.evidence.pageInfo);
  }
  return evidence;
}

export async function waitForReport(api: LocalGraphQL, inspectionId: string): Promise<{ id: string; report: Record<string, unknown>; evidence: Array<Record<string, unknown>> }> {
  const started = Date.now();
  await expect.poll(async () => {
    // Até o snapshot existir, a API responde "report is not ready" em vez de null.
    const data = await api.execute<{ report: { id: string; requirements: Array<{ analysisStatus: string }>; evidence: { nodes: Array<{ id: string }> } } | null }>(
      `query VideoE2EReportReady($inspectionId: ID!) { report(inspectionId: $inspectionId) { id requirements { analysisStatus } evidence(first: 1) { nodes { id } } } }`, { inspectionId })
      .catch((error: Error) => {
        if (error.message.includes("report is not ready")) return { report: null };
        throw error;
      });
    if (data.report) {
      if (data.report.requirements.some((item) => ["FAILED", "INCONCLUSIVE"].includes(item.analysisStatus))) throw new Error(`Análise do laudo ${data.report.id} terminou sem sucesso: ${JSON.stringify(data.report.requirements)}`);
      if (data.report.requirements.length && data.report.requirements.every((item) => item.analysisStatus === "COMPLETED") && data.report.evidence.nodes.length) return true;
    }
    if (Date.now() - started > 18 * 60_000) throw new Error(`Laudo da vistoria ${inspectionId} não concluiu as análises em 18 minutos.`);
    return false;
  }, { timeout: 19 * 60_000, intervals: [2_000, 5_000, 10_000], message: `laudo ${inspectionId} e análises dos frames` }).toBe(true);
  const result = await api.execute<{ report: { id: string; mode: string; requirements: Array<Record<string, unknown>>; evidence: ReportEvidencePage } | null }>(
    `query VideoE2EReport($inspectionId: ID!) { report(inspectionId: $inspectionId) { id mode requirements { key analysisMode analysisStatus coverage noRelevantChange } evidence(first: 100) { ${reportEvidenceFields} } } }`, { inspectionId });
  if (!result.report) throw new Error(`Laudo ${inspectionId} desapareceu após a conclusão.`);
  const evidence = await remainingReportEvidence(api, inspectionId, result.report.evidence);
  return { id: result.report.id, report: result.report as unknown as Record<string, unknown>, evidence };
}

export async function assertLiveAI(api: LocalGraphQL, inspectionId: string, requiredModes: string[]): Promise<Array<Record<string, unknown>>> {
  const calls: Array<Record<string, unknown>> = [];
  let after: string | null = null;
  let hasNext = true;
  while (hasNext) {
    const data: { inspectionLLMUsage: { mode: string; calls: Array<Record<string, unknown> & { mode: string; comparisonMode: string; technicalOutcome: string; imageCount: number | null; state: string }>; pageInfo: { endCursor: string | null; hasNextPage: boolean } } } = await api.execute(
      `query VideoE2ELLMUsage($inspectionId: ID!, $after: String) { inspectionLLMUsage(inspectionId: $inspectionId, mode: LIVE, first: 100, after: $after) { mode calls { mode comparisonMode modelAlias provider model state technicalOutcome transportDelivered httpStatus imageCount inputTokens outputTokens startedAt finishedAt } pageInfo { endCursor hasNextPage } } }`, { inspectionId, after });
    expect(data.inspectionLLMUsage.mode).toBe("LIVE");
    calls.push(...data.inspectionLLMUsage.calls);
    after = data.inspectionLLMUsage.pageInfo.endCursor;
    hasNext = data.inspectionLLMUsage.pageInfo.hasNextPage;
  }
  const successful = calls.filter((call) => call.mode === "LIVE" && call.state === "FINISHED" && call.technicalOutcome === "success" && call.transportDelivered === true && Number(call.imageCount) > 0);
  for (const mode of requiredModes) expect(successful.some((call) => call.comparisonMode === mode), `chamada LIVE de ${mode} com imagens: ${JSON.stringify(calls)}`).toBe(true);
  return calls;
}

export async function openReportAndCheckPDF(page: Page, request: APIRequestContext, api: LocalGraphQL, scenario: VideoScenario, report: { id: string; report: Record<string, unknown>; evidence: Array<Record<string, unknown>> }, testInfo: TestInfo): Promise<void> {
  scenario.reportId = report.id;
  const latestVideoFrame = report.evidence.find((item) => item.evidenceKind === "VIDEO_FRAME" && item.availability === "AVAILABLE" && typeof item.url === "string");
  expect(latestVideoFrame, "laudo com ao menos um frame disponível").toBeTruthy();
  const expectedSources = [scenario.currentMediaId!, ...(scenario.originMediaId ? [scenario.originMediaId] : [])];
  for (const sourceMediaId of expectedSources) expect(report.evidence.some((item) => item.evidenceKind === "VIDEO_FRAME" && item.sourceMediaId === sourceMediaId), `frames do vídeo ${sourceMediaId} vinculados ao laudo`).toBe(true);
  const requirement = (report.report.requirements as Array<{ analysisMode: string; analysisStatus: string }>);
  expect(requirement.every((item) => item.analysisStatus === "COMPLETED"), "todos os itens de análise do laudo concluídos").toBe(true);

  await api.login("/reports");
  const search = page.getByRole("search").getByRole("searchbox", { name: "Buscar laudo" });
  await search.fill(scenario.assetName);
  await expect(page.getByRole("button", { name: `Abrir detalhes do laudo de ${scenario.assetName}` })).toBeVisible({ timeout: 30_000 });
  await page.getByRole("button", { name: `Abrir detalhes do laudo de ${scenario.assetName}` }).click();
  const dialog = page.getByRole("dialog", { name: "Laudo de vistoria" });
  await expect(dialog.getByRole("heading", { name: scenario.assetName })).toBeVisible();
  await expect(dialog.getByRole("heading", { name: "Vídeo do imóvel" })).toBeVisible();
  const currentFrame = dialog.locator('img[alt*="Vistoria atual"], img[alt*="Fotos da vistoria"]').first();
  await expect(currentFrame).toBeVisible({ timeout: 60_000 });
  await expect.poll(() => currentFrame.evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth > 0), { timeout: 60_000, message: "frame de vídeo carregado no laudo" }).toBe(true);
  const play = dialog.getByRole("button", { name: /Reproduzir vídeo no ponto/ }).first();
  await expect(play).toBeVisible();
  await play.click();
  const video = dialog.locator("video").first();
  await expect(video).toBeVisible({ timeout: 30_000 });
  await expect.poll(() => video.evaluate((element: HTMLVideoElement) => element.readyState >= 1), { timeout: 60_000, message: "stream do vídeo original carregado" }).toBe(true);
  await video.evaluate(async (element: HTMLVideoElement) => { element.muted = true; await element.play(); });
  await expect.poll(() => video.evaluate((element: HTMLVideoElement) => !element.paused && element.currentTime > 0), { timeout: 30_000, message: "reprodução do vídeo original avançou" }).toBe(true);

  await dialog.getByRole("button", { name: "Preparar PDF" }).click();
  const started = Date.now();
  let download: { url: string; status: string; sha256: string | null } | null = null;
  while (Date.now() - started < 4 * 60_000) {
    const data: { reportDownload: { url: string; status: string; sha256: string | null } | null } = await api.execute(
      `query VideoE2EPDF($snapshotId: ID!) { reportDownload(snapshotId: $snapshotId) { url status sha256 } }`, { snapshotId: report.id });
    download = data.reportDownload;
    if (download?.status === "READY" && download.url) break;
    if (download && ["FAILED", "UNAVAILABLE"].includes(download.status)) throw new Error(`PDF do laudo ${report.id} falhou: ${download.status}`);
    await page.waitForTimeout(3_000);
  }
  expect(download?.status, "PDF do laudo chegou a READY").toBe("READY");
  expect(download?.sha256, "PDF fornece SHA-256").toMatch(/^[a-f0-9]{64}$/i);
  await expect(dialog.getByRole("link", { name: /Baixar PDF/ })).toBeVisible({ timeout: 60_000 });
  const pdfResponse = await request.get(download!.url, { timeout: 60_000 });
  expect(pdfResponse.ok(), `download PDF respondeu ${pdfResponse.status()}`).toBe(true);
  const bytes = await pdfResponse.body();
  expect(bytes.subarray(0, 5).toString("ascii")).toBe("%PDF-");
  expect(createHash("sha256").update(bytes).digest("hex")).toBe(download!.sha256!.toLowerCase());
  const { evidence: _embeddedEvidence, ...reportSummary } = report.report;
  const safeReport = { id: report.id, report: reportSummary, evidence: report.evidence.map(({ url: _url, ...item }) => item) };
  await testInfo.attach(`${scenario.id}-report.json`, { body: JSON.stringify(safeReport, null, 2), contentType: "application/json" });
}

export async function diagnostics(testInfo: TestInfo, page: Page, scenario: VideoScenario, extra: Record<string, unknown>, api?: LocalGraphQL): Promise<void> {
  const snapshot: Record<string, unknown> = {};
  if (api) {
    for (const [label, mediaId] of [["currentPreparation", scenario.currentMediaId], ["originPreparation", scenario.originMediaId]] as const) {
      if (!mediaId) continue;
      try {
        const result = await api.execute<{ videoPreparation: { status: string; stage: string; errorCode: string; preparationId: string; processedFrames: number } }>(
          `query VideoE2EDiagnosticPreparation($mediaId: ID!) { videoPreparation(mediaId: $mediaId) { status stage errorCode preparationId processedFrames } }`, { mediaId });
        snapshot[label] = result.videoPreparation;
      } catch (error) { snapshot[label] = { error: (error as Error).message }; }
    }
    if (scenario.inspectionId) {
      try {
        const result = await api.execute<{ report: { id: string; requirements: Array<{ key: string; analysisMode: string; analysisStatus: string; noRelevantChange: boolean | null }>; evidence: { nodes: Array<{ id: string; role: string; evidenceKind: string; sourceMediaId: string | null; preparationId: string | null }>; pageInfo: { endCursor: string | null; hasNextPage: boolean } } } | null }>(
          `query VideoE2EDiagnosticReport($inspectionId: ID!) { report(inspectionId: $inspectionId) { id requirements { key analysisMode analysisStatus noRelevantChange } evidence(first: 100) { nodes { id role evidenceKind sourceMediaId preparationId } pageInfo { endCursor hasNextPage } } } }`, { inspectionId: scenario.inspectionId });
        snapshot.report = result.report;
      } catch (error) { snapshot.report = { error: (error as Error).message }; }
      try {
        const result = await api.execute<{ inspectionLLMUsage: { mode: string; calls: Array<{ comparisonMode: string; state: string; technicalOutcome: string; transportDelivered: boolean; imageCount: number | null }>; pageInfo: { endCursor: string | null; hasNextPage: boolean } } }>(
          `query VideoE2EDiagnosticLLM($inspectionId: ID!) { inspectionLLMUsage(inspectionId: $inspectionId, mode: LIVE, first: 100) { mode calls { comparisonMode state technicalOutcome transportDelivered imageCount } pageInfo { endCursor hasNextPage } } }`, { inspectionId: scenario.inspectionId });
        snapshot.llmUsage = { mode: result.inspectionLLMUsage.mode, calls: result.inspectionLLMUsage.calls };
      } catch (error) { snapshot.llmUsage = { error: (error as Error).message }; }
    }
  }
  await testInfo.attach(`${scenario.id || "video-e2e"}-diagnostics.json`, { body: JSON.stringify({ ...scenario, ...extra, snapshot }, null, 2), contentType: "application/json" });
  try { await testInfo.attach(`${scenario.id}-screenshot.png`, { body: await page.screenshot({ fullPage: true }), contentType: "image/png" }); } catch { /* The browser may already have closed. */ }
}
