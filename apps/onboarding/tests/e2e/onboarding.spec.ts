import { test } from "@playwright/test";
import { OnboardingPage } from "./onboarding-page";

test("completes the real onboarding flow and creates the first vistoria", async ({ page }, testInfo) => {
  const onboarding = new OnboardingPage(page);
  const suffix = `${Date.now()}-${testInfo.project.name.replace(/[^a-z0-9]/gi, "-")}`;
  const deadline = new Date(Date.now() + 7 * 24 * 60 * 60 * 1000).toISOString().slice(0, 10);

  await onboarding.open();
  await onboarding.start("Ana E2E", `ana.${suffix}@example.test`);
  await onboarding.verify("654321");
  await onboarding.expectStep("Imobiliária");
  await onboarding.saveAgency(`Imobiliária E2E ${suffix}`);
  await onboarding.expectStep("Imóvel");

  // A full reload proves that the HttpOnly session can restore a fresh CSRF
  // proof and continue from the next server-confirmed step.
  await page.reload();
  await onboarding.expectStep("Imóvel");
  await onboarding.saveProperty({ address: "Rua do Fluxo, 123", propertyType: "APARTMENT", rooms: "3", purpose: "RENTAL", deadline });
  await onboarding.expectStep("Fotos de referência");
  await onboarding.saveChecklistOrigin();
  await onboarding.expectStep("Responsável pela vistoria");
  await onboarding.saveSelfParticipant();
  await onboarding.submit();
  await onboarding.expectCreated();
});
