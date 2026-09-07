const encoder = new TextEncoder();

function randomVerifier(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(32));
  return btoa(String.fromCharCode(...bytes)).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
}

function randomState(): string {
  return randomVerifier();
}

async function challenge(verifier: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", encoder.encode(verifier));
  return btoa(String.fromCharCode(...new Uint8Array(digest))).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
}

export async function beginPKCE(authorizeEndpoint: string, clientId: string, redirectUri: string): Promise<void> {
	const verifier = randomVerifier();
	const state = randomState();
	// PKCE verifier is a one-time navigation secret, never an access credential.
	sessionStorage.setItem("inspection.pkce.verifier", verifier);
	sessionStorage.setItem("inspection.pkce.state", state);
  const url = new URL(authorizeEndpoint);
  url.searchParams.set("response_type", "code");
  url.searchParams.set("client_id", clientId);
  url.searchParams.set("redirect_uri", redirectUri);
  url.searchParams.set("code_challenge_method", "S256");
	url.searchParams.set("code_challenge", await challenge(verifier));
	url.searchParams.set("state", state);
	location.assign(url);
}

// takePKCEVerifier consumes the one-time verifier only when the OIDC callback
// returned the state created for this browser tab. A forged callback cannot
// exchange a code issued to another login attempt.
export function takePKCEVerifier(callbackState: string | null): string | undefined {
	const verifier = sessionStorage.getItem("inspection.pkce.verifier") ?? undefined;
	const state = sessionStorage.getItem("inspection.pkce.state");
	sessionStorage.removeItem("inspection.pkce.verifier");
	sessionStorage.removeItem("inspection.pkce.state");
	return callbackState && state === callbackState ? verifier : undefined;
}
