import type { OnboardingChoice } from "./presentation";

export type OnboardingField = { key: string; label: string; type: string; required: boolean; placeholder: string | null; options: string[]; choices?: OnboardingChoice[] };
export type OnboardingStep = { key: string; label: string; position: number; required: boolean; fields: OnboardingField[] };
export type OnboardingDefinition = { schemaVersion: number; version: number; segment: string; segmentVersion: string; steps: OnboardingStep[]; originModes: Array<{ key: string; label: string; templateKey: string; required: boolean }> };
export type StepValues = Record<string, string | string[]>;
export type OnboardingOwner = { name: string; email: string };

export function valuesForParticipantMode(values: StepValues, mode: string, owner: OnboardingOwner): StepValues {
  return mode === "SELF" ? { ...values, mode, name: owner.name, email: owner.email } : { ...values, mode };
}

export function isSupportedDefinition(definition: OnboardingDefinition) {
  return definition.schemaVersion === 1
    && definition.segment === "REAL_ESTATE"
    && definition.steps.length > 0
    && definition.steps.every((step) => step.key.trim() !== "" && step.label.trim() !== "" && Number.isInteger(step.position) && Array.isArray(step.fields));
}

export function sortedSteps(definition: OnboardingDefinition) {
  return [...definition.steps].sort((left, right) => left.position - right.position);
}

export function validateStep(step: OnboardingStep, values: StepValues) {
  const textValue = (key: string) => typeof values[key] === "string" ? values[key] as string : "";
  return step.fields.reduce<Record<string, string>>((errors, field) => {
    const value = textValue(field.key);
    if (field.required && !value) errors[field.key] = "Preencha este campo para continuar.";
    if (field.type === "email" && value && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value)) errors[field.key] = "Informe um e-mail válido.";
    if (field.key === "mode" && value === "DELEGATE") {
      if (!textValue("name").trim()) errors.name = "Informe o nome da pessoa responsável.";
      if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(textValue("email"))) errors.email = "Informe o e-mail da pessoa responsável.";
      if (textValue("email").trim().toLowerCase() !== textValue("emailConfirmation").trim().toLowerCase()) errors.emailConfirmation = "Os e-mails precisam ser iguais.";
    }
    return errors;
  }, {});
}

export function resolveResume<T extends { currentStep: string }>(server: T | undefined, draft: { step: string } | undefined) {
  return server ? server.currentStep : draft?.step;
}

/** Returns the next step after the server's last confirmed checkpoint. */
export function nextConfirmedStep(definition: OnboardingDefinition, session: { state: string; currentStep: string }) {
  const steps = sortedSteps(definition);
  if (session.state === "IDENTITY_VERIFIED") return steps[0]?.key ?? "";
  if (session.state === "PARTICIPANT_SAVED" || session.state === "READY_TO_SUBMIT" || session.state === "SUBMITTED") return "";
  const currentIndex = steps.findIndex((step) => step.key.toLowerCase() === session.currentStep.toLowerCase());
  return currentIndex >= 0 ? steps[currentIndex + 1]?.key ?? "" : steps[0]?.key ?? "";
}
