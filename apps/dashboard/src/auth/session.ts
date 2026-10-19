"use client";

export type DashboardIdentity = { tenantId: string; tenantName: string; tenantStatus: string; entitlements: string[]; roles: string[]; scopes: Array<{ kind: string; resourceId: string }> };
const membershipStorageKey = "inspection.dashboard.membership";
function readStoredMembership(): string | undefined {
  if (typeof window === "undefined") return undefined;
  return window.sessionStorage.getItem(membershipStorageKey) ?? undefined;
}
let token: string | undefined;
let idToken: string | undefined;
let refreshToken: string | undefined;
let expiresAt = 0;
let refreshRequest: Promise<string | undefined> | undefined;
let sessionVersion = 0;
let identity: DashboardIdentity | undefined;
let membershipId: string | undefined = readStoredMembership();
let protectedStateGeneration = 0;
export function setSession(nextToken: string, nextIdentity?: DashboardIdentity, nextIDToken?: string, nextRefreshToken?: string, expiresIn?: number): void { token = nextToken; idToken = nextIDToken; refreshToken = nextRefreshToken; expiresAt = expiresIn ? Date.now() + expiresIn * 1000 : 0; refreshRequest = undefined; sessionVersion += 1; identity = nextIdentity; membershipId = readStoredMembership(); protectedStateGeneration += 1; }
export function setIdentity(nextIdentity: DashboardIdentity): void { identity = nextIdentity; }
export function getAccessToken(): string | undefined { return token; }
export function getSessionVersion(): number { return sessionVersion; }
/** Renews an expiring access token without persisting either credential in browser storage. */
export async function accessTokenForRequest(staleToken?: string): Promise<string | undefined> {
  if (!token) return undefined;
  if (!refreshToken || (staleToken === undefined && (!expiresAt || expiresAt - Date.now() > 30_000))) return token;
  if (staleToken && token !== staleToken) return token;
  if (refreshRequest) return refreshRequest;
  const endpoint = process.env.NEXT_PUBLIC_OIDC_TOKEN_URL;
  if (!endpoint) return token;
  const version = sessionVersion;
  const currentRefreshToken = refreshToken;
  const request = (async () => {
    const response = await fetch(endpoint, { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ grant_type: "refresh_token", refresh_token: currentRefreshToken, client_id: "inspection-dashboard" }) });
    if (!response.ok) {
      if (version === sessionVersion && (response.status === 400 || response.status === 401)) clearSession();
      throw new Error("Não foi possível renovar a sessão. Tente novamente.");
    }
    const renewed = await response.json() as { access_token?: string; refresh_token?: string; id_token?: string; expires_in?: number };
    if (!renewed.access_token) throw new Error("Resposta inválida ao renovar a sessão.");
    if (version !== sessionVersion) return undefined;
    token = renewed.access_token;
    refreshToken = renewed.refresh_token ?? currentRefreshToken;
    if (renewed.id_token) idToken = renewed.id_token;
    expiresAt = renewed.expires_in ? Date.now() + renewed.expires_in * 1000 : 0;
    return token;
  })();
  refreshRequest = request;
  try { return await request; } finally { if (refreshRequest === request) refreshRequest = undefined; }
}
export function getIDToken(): string | undefined { return idToken; }
export function getIdentity(): DashboardIdentity | undefined { return identity; }
export function getMembershipId(): string | undefined { return membershipId; }
export function getProtectedStateGeneration(): number { return protectedStateGeneration; }
export function selectMembership(nextMembershipId: string): void { if (membershipId !== nextMembershipId) { membershipId = nextMembershipId; window.sessionStorage.setItem(membershipStorageKey, nextMembershipId); identity = undefined; protectedStateGeneration += 1; } }
export function clearSession(): void { token = undefined; idToken = undefined; refreshToken = undefined; expiresAt = 0; refreshRequest = undefined; sessionVersion += 1; identity = undefined; membershipId = undefined; window.sessionStorage.removeItem(membershipStorageKey); protectedStateGeneration += 1; }
export function hasDashboardAccess(current = identity): boolean { return Boolean(current?.tenantStatus === "ACTIVE" && current.entitlements.includes("DASHBOARD") && current.roles.length); }
export function isSuperAdmin(current: Pick<DashboardIdentity, "entitlements" | "roles"> | undefined = identity): boolean {
  return Boolean(
    current?.roles.includes("TENANT_ADMIN")
      && current.entitlements.includes("ADMIN")
      && current.entitlements.includes("DASHBOARD"),
  );
}
