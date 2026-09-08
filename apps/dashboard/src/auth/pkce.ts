import { safeDashboardPath } from "@/auth/return-path";

const encoder = new TextEncoder();
const key = "inspection.dashboard.pkce";
const random = () => btoa(String.fromCharCode(...crypto.getRandomValues(new Uint8Array(32)))).replace(/[+/=]/g, (value) => ({ "+": "-", "/": "_", "=": "" })[value]!);
async function challenge(verifier: string): Promise<string> { return btoa(String.fromCharCode(...new Uint8Array(await crypto.subtle.digest("SHA-256", encoder.encode(verifier))))).replace(/[+/=]/g, (value) => ({ "+": "-", "/": "_", "=": "" })[value]!); }

export async function beginPKCE(authorizeEndpoint: string, returnTo = "/tenants/current"): Promise<void> {
  const verifier = random(); const state = random();
  sessionStorage.setItem(key, JSON.stringify({ verifier, state, returnTo: safeDashboardPath(returnTo) }));
  const url = new URL(authorizeEndpoint);
  url.searchParams.set("response_type", "code"); url.searchParams.set("client_id", "inspection-dashboard"); url.searchParams.set("redirect_uri", `${location.origin}/auth/callback`); url.searchParams.set("code_challenge_method", "S256"); url.searchParams.set("code_challenge", await challenge(verifier)); url.searchParams.set("state", state);
  location.assign(url.toString());
}

export function takePKCE(callbackState: string | null): { verifier: string; returnTo: string } | undefined {
  const serialized = sessionStorage.getItem(key);
  if (!serialized || !callbackState) return undefined;
  try {
    const saved = JSON.parse(serialized) as { verifier?: string; state?: string; returnTo?: string };
    if (saved.state !== callbackState || !saved.verifier) return undefined;
    sessionStorage.removeItem(key);
    return { verifier: saved.verifier, returnTo: safeDashboardPath(saved.returnTo) };
  } catch { return undefined; }
}
