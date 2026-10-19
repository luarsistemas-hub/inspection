"use client";

// The HttpOnly external-session cookie is the credential. This proof is memory
// only so a reload cannot accidentally restore authorization after revocation.
let csrfToken: string | undefined;
const captureLinkTokenKey = "inspection.capture.link-token";
const csrfChannel = typeof window !== "undefined" && typeof BroadcastChannel !== "undefined"
  ? new BroadcastChannel("inspection.capture.csrf")
  : undefined;

csrfChannel?.addEventListener("message", (event: MessageEvent<{ type?: string; token?: string }>) => {
  if (event.data?.type === "verified" && typeof event.data.token === "string") csrfToken = event.data.token;
});

export const setCaptureCsrfToken = (token: string): void => { csrfToken = token; csrfChannel?.postMessage({ type: "verified", token }); };
export const getCaptureCsrfToken = (): string | undefined => csrfToken;
export const clearCaptureCsrfToken = (): void => { csrfToken = undefined; };

export const clearCaptureSession = (): void => { clearCaptureCsrfToken(); clearCaptureLinkToken(); };

export const setCaptureLinkToken = (token: string): void => { sessionStorage.setItem(captureLinkTokenKey, token); };
export const getCaptureLinkToken = (): string | undefined => sessionStorage.getItem(captureLinkTokenKey) ?? undefined;
export const clearCaptureLinkToken = (): void => { sessionStorage.removeItem(captureLinkTokenKey); };
