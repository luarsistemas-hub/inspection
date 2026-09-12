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
}
