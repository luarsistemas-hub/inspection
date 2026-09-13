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
    await this.page.getByLabel("Agency name").fill(name);
    await this.saveStep();
  }

  async saveProperty(input: { address: string; propertyType: string; rooms: string; purpose: string; deadline: string }) {
    await this.page.getByLabel("Property address").fill(input.address);
    await this.page.getByLabel("Property type").selectOption(input.propertyType);
    await this.page.getByLabel("Rooms").fill(input.rooms);
    await this.page.getByLabel("Purpose").selectOption(input.purpose);
    await this.page.getByLabel("Inspection deadline").fill(input.deadline);
    await this.saveStep();
  }

  async saveChecklistOrigin() {
    await this.page.getByLabel("Reference mode").selectOption("CHECKLIST_ONLY");
    await this.saveStep();
  }

  async saveSelfParticipant() {
    await this.page.getByLabel("Who will inspect").selectOption("SELF");
    await this.saveStep();
  }

  async submit() {
    await expect(this.page.getByRole("heading", { name: "Revise a solicitação" })).toBeVisible();
    await expect(this.page.getByText("Agency: name")).toBeVisible();
    await this.page.getByRole("button", { name: "Criar primeira inspeção" }).click();
  }

  async expectCreated() {
    await expect(this.page.getByRole("heading", { name: "Acompanhe a solicitação" })).toBeVisible();
    await expect(this.page.getByText("Primeira inspeção criada")).toBeVisible();
    await expect(this.page.getByText(/inspeção [0-9a-f-]{36}/i)).toBeVisible();
  }

  private async saveStep() {
    await this.page.getByRole("button", { name: "Salvar e continuar" }).click();
  }
}
