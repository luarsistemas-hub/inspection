let csrfToken: string | undefined;

/** Stores only the short-lived CSRF proof received in a response header. Cookies stay HttpOnly. */
export function setOnboardingCsrfToken(value: string | null | undefined) {
  csrfToken = value || undefined;
}

export function getOnboardingCsrfToken() { return csrfToken; }

export function clearOnboardingSession() { csrfToken = undefined; }
