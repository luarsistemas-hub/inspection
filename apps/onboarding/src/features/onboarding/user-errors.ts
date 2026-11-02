export type OnboardingUserError = { message: string; field?: string | null };

/** Maps errors for visible controls and returns the first unmapped error for the page alert. */
export function routeOnboardingUserErrors(errors: readonly OnboardingUserError[], visibleFields: readonly string[]) {
  const visible = new Set(visibleFields);
  const fieldErrors: Record<string, string> = {};
  for (const error of errors) {
    if (error.field && visible.has(error.field)) fieldErrors[error.field] = error.message;
  }
  return { fieldErrors, generalError: errors.find((item) => !item.field || !visible.has(item.field))?.message };
}
