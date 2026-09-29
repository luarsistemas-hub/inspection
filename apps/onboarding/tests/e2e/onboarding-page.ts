import { expect, type Page } from "@playwright/test";

export class OnboardingPage {
  constructor(private readonly page: Page) {}

  async open() { await this.page.goto("/"); }
  async start(name: string, email: string) {
    await this.page.getByLabel("Seu nome").fill(name);
    await this.page.getByLabel("Seu e-mail").fill(email);
    await this.page.getByRole("button", { name: "Enviar código" }).click();
  }
  async verify(code: string) {
    await this.page.getByLabel("Código de confirmação").fill(code);
    await this.page.getByRole("button", { name: "Confirmar e continuar" }).click();
  }
  async expectStep(label: string) { await expect(this.page.getByRole("heading", { name: label })).toBeVisible(); }

  async saveAgency(name: string) {
    await this.page.getByLabel("Nome da imobiliária").fill(name);
    await this.saveStep();
  }

  async saveProperty(input: { address: string; propertyType: string; purpose: string; deadline: string }) {
    await this.page.getByLabel("Endereço do imóvel").fill(input.address);
    await this.page.getByLabel("Tipo de imóvel").selectOption(input.propertyType);
    await this.page.getByLabel("Finalidade da vistoria").selectOption(input.purpose);
    await this.page.getByLabel("Prazo para concluir a vistoria").fill(input.deadline);
    await this.saveStep();
  }

  async saveChecklistOrigin() {
    await this.page.getByLabel("Base de comparação").selectOption("CHECKLIST_ONLY");
    await this.saveStep();
  }

  async saveSelfParticipant(name: string, email: string) {
    await this.page.getByLabel("Quem realizará a vistoria?").selectOption("SELF");
    await expect(this.page.getByLabel("Nome do responsável")).toHaveValue(name);
    await expect(this.page.getByLabel("E-mail do responsável")).toHaveValue(email);
    await expect(this.page.getByLabel("Nome do responsável")).toBeEditable();
    await expect(this.page.getByLabel("E-mail do responsável")).toBeEditable();
    await this.saveStep();
  }

  async submit() {
    await expect(this.page.getByRole("heading", { name: "Revise os dados" })).toBeVisible();
    await expect(this.page.getByText("Imobiliária: Nome da imobiliária")).toBeVisible();
    await this.page.getByRole("button", { name: "Criar primeira vistoria" }).click();
  }

  async expectCreated() {
    await expect(this.page.getByRole("heading", { name: "Acompanhe a vistoria" })).toBeVisible();
    await expect(this.page.getByText(/A vistoria .* foi criada\./)).toBeVisible();
    await expect(this.page.getByText(/vistoria [0-9a-f-]{36}/i)).toBeVisible();
  }

  private async saveStep() {
    await this.page.getByRole("button", { name: "Salvar e continuar" }).click();
  }
}
