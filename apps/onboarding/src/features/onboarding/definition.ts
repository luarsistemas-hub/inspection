export type OnboardingField = { key: string; label: string; type: string; required: boolean; placeholder: string | null; options: string[] };
export type OnboardingStep = { key: string; label: string; position: number; required: boolean; fields: OnboardingField[] };
export type OnboardingDefinition = { schemaVersion: number; segment: string; steps: OnboardingStep[]; originModes: Array<{ key: string; label: string; required: boolean }> };
export type StepValues = Record<string, string>;

export function isSupportedDefinition(definition: OnboardingDefinition) {
  return definition.schemaVersion === 1 && definition.segment === "REAL_ESTATE" && definition.steps.length > 0;
}

export function sortedSteps(definition: OnboardingDefinition) {
  return [...definition.steps].sort((left, right) => left.position - right.position);
}

export function validateStep(step: OnboardingStep, values: StepValues) {
  return step.fields.reduce<Record<string, string>>((errors, field) => {
    const value = values[field.key]?.trim() ?? "";
    if (field.required && !value) errors[field.key] = "Preencha este campo para continuar.";
    if (field.type === "email" && value && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value)) errors[field.key] = "Informe um e-mail válido.";
    if (field.key === "mode" && value === "DELEGATE") {
      if (!values.name?.trim()) errors.name = "Informe o nome da pessoa responsável.";
      if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(values.email ?? "")) errors.email = "Informe o e-mail da pessoa responsável.";
    }
    return errors;
  }, {});
}

export function resolveResume<T extends { currentStep: string }>(server: T | undefined, draft: { step: string } | undefined) {
  return server ? server.currentStep : draft?.step;
}
