import { expect, test, type APIRequestContext } from "@playwright/test";

type MailSummary = { ID: string; Subject: string; To: Array<{ Address: string }> };
type MailList = { messages: MailSummary[] };
type MailDetail = { Text: string };

const onboardingURL = process.env.PLAYWRIGHT_ONBOARDING_URL ?? "http://localhost:3004";
const mailpitURL = process.env.PLAYWRIGHT_MAILPIT_URL ?? "http://localhost:8026";

async function mailText(request: APIRequestContext, email: string, subject: string): Promise<string> {
  const listResponse = await request.get(`${mailpitURL}/api/v1/messages?limit=100`);
  if (!listResponse.ok()) return "";
  const list = await listResponse.json() as MailList;
  const message = list.messages.find((candidate) => candidate.Subject === subject && candidate.To.some((recipient) => recipient.Address === email));
  if (!message) return "";
  const detailResponse = await request.get(`${mailpitURL}/api/v1/message/${message.ID}`);
  if (!detailResponse.ok()) return "";
  return ((await detailResponse.json()) as MailDetail).Text ?? "";
}

test("E2E-011 activates an owner from the real invitation before normal PKCE login", async ({ page, request }, testInfo) => {
  const suffix = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const email = `activation.${suffix}@example.test`;
  const deadline = new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);

  await page.goto(onboardingURL);
  await page.getByLabel("Seu nome").fill("Ana Ativação E2E");
  await page.getByLabel("Seu e-mail").fill(email);
  await page.getByRole("button", { name: "Enviar código" }).click();
  await page.getByLabel("Código de confirmação").fill("654321");
  await page.getByRole("button", { name: "Confirmar e continuar" }).click();
  await page.getByLabel("Nome da imobiliária").fill(`Imobiliária Ativação ${suffix}`);
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await page.getByLabel("Endereço do imóvel").fill("Rua da Ativação, 123");
  await page.getByLabel("Tipo de imóvel").selectOption("APARTMENT");
  await page.getByLabel("Quantidade de cômodos").fill("3");
  await page.getByLabel("Finalidade da vistoria").selectOption("RENTAL");
  await page.getByLabel("Prazo para concluir a vistoria").fill(deadline);
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await page.getByLabel("Base de comparação").selectOption("CHECKLIST_ONLY");
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await page.getByLabel("Quem realizará a vistoria?").selectOption("SELF");
  await page.getByRole("button", { name: "Salvar e continuar" }).click();
  await page.getByRole("button", { name: "Criar primeira vistoria" }).click();
  await expect(page.getByText("Primeira vistoria criada")).toBeVisible();

  let activationURL = "";
  await expect.poll(async () => {
    const text = await mailText(request, email, "Ative seu acesso administrativo | Inspection");
    activationURL = text.match(/http:\/\/localhost:3000\/activate\?token=[^\s]+/)?.[0] ?? "";
    return activationURL;
  }).toMatch(/^http:\/\/localhost:3000\/activate\?token=/);

  await page.goto(activationURL);
  await page.getByRole("button", { name: "Enviar código de ativação" }).click();
  await expect(page).toHaveURL(/\/activate$/);
  await page.reload();
  await page.getByRole("button", { name: "Enviar código de ativação" }).click();
  await page.getByLabel("Código de ativação").fill("654321");
  await page.getByRole("button", { name: "Confirmar código" }).click();
  const password = `Senha-${suffix}-forte!`;
  await page.getByLabel("Nova senha", { exact: true }).fill(password);
  await page.getByLabel("Confirme a nova senha").fill(password);
  await page.getByRole("button", { name: "Criar senha" }).click();
  await expect(page.getByRole("button", { name: "Entrar na administração" })).toBeVisible();
});
