"use client";

// The external CSRF proof is intentionally kept in memory only. The HttpOnly
// session cookie remains the server credential; a reload must obtain a fresh
// proof through the OTP flow instead of reusing browser storage.
let csrfToken: string | undefined;

export function setCaptureCsrfToken(token: string): void {
  csrfToken = token;
}

export function getCaptureCsrfToken(): string | undefined {
  return csrfToken;
}

export function clearCaptureCsrfToken(): void {
  csrfToken = undefined;
}
