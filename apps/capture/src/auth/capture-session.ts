"use client";

// The HttpOnly external-session cookie is the credential. This proof is memory
// only so a reload cannot accidentally restore authorization after revocation.
let csrfToken: string | undefined;

export const setCaptureCsrfToken = (token: string): void => { csrfToken = token; };
export const getCaptureCsrfToken = (): string | undefined => csrfToken;
export const clearCaptureCsrfToken = (): void => { csrfToken = undefined; };
