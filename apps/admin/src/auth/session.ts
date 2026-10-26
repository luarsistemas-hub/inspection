"use client";

export type AdminIdentity = { tenantId: string; tenantName: string; entitlements: string[]; roles: string[]; membershipId?: string; scope?: string };

let token: string | undefined;
let idToken: string | undefined;
let refreshToken: string | undefined;
let expiresAt = 0;
let refreshRequest: Promise<string | undefined> | undefined;
let sessionVersion = 0;
let identity: AdminIdentity | undefined;

export function setSession(nextToken: string, nextIdentity?: AdminIdentity, nextIDToken?: string, nextRefreshToken?: string, expiresIn?: number): void { token = nextToken; idToken = nextIDToken; refreshToken = nextRefreshToken; expiresAt = expiresIn ? Date.now() + expiresIn * 1000 : 0; refreshRequest = undefined; sessionVersion += 1; identity = nextIdentity; }
export function setIdentity(nextIdentity: AdminIdentity): void { identity = nextIdentity; }
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
    const response = await fetch(endpoint, { method: "POST", headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: new URLSearchParams({ grant_type: "refresh_token", refresh_token: currentRefreshToken, client_id: "inspection-admin" }) });
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
export function getIdentity(): AdminIdentity | undefined { return identity; }
export function clearSession(): void { token = undefined; idToken = undefined; refreshToken = undefined; expiresAt = 0; refreshRequest = undefined; sessionVersion += 1; identity = undefined; }
const membershipKey = "inspection.admin.membership";

export function setMembershipContext(membershipId: string, scope = "Tenant"): void {
  identity = identity ? { ...identity, membershipId, scope } : identity;
  sessionStorage.setItem(membershipKey, JSON.stringify({ membershipId, scope }));
}

export function restoreMembershipContext(): { membershipId: string; scope: string } | undefined {
  const stored = sessionStorage.getItem(membershipKey);
  if (!stored) return undefined;
  try { return JSON.parse(stored) as { membershipId: string; scope: string }; } catch { sessionStorage.removeItem(membershipKey); return undefined; }
}

export function clearProtectedContext(): void {
  sessionStorage.removeItem(membershipKey);
  identity = identity ? { ...identity, membershipId: undefined, scope: undefined } : undefined;
}
export function hasAdminAccess(current = identity): boolean {
  return Boolean(
    current?.entitlements.includes("ADMIN")
      && current.roles.some((role) => ["TENANT_ADMIN", "ORGANIZATION_ADMIN", "ACCESS_ADMIN", "PARTICIPATION_ADMIN", "INSPECTION_CONFIG_ADMIN", "GOVERNANCE_ADMIN", "AUDITOR"].includes(role)),
  );
}

const adminPageRoles: Record<string, string[]> = {
  "/organization": ["TENANT_ADMIN", "ORGANIZATION_ADMIN"],
  "/access": ["TENANT_ADMIN", "ORGANIZATION_ADMIN", "ACCESS_ADMIN"],
  "/catalogs": ["TENANT_ADMIN", "PARTICIPATION_ADMIN"],
  "/assets": ["TENANT_ADMIN", "INSPECTION_CONFIG_ADMIN"],
  "/templates": ["TENANT_ADMIN", "INSPECTION_CONFIG_ADMIN"],
  "/video": ["TENANT_ADMIN", "INSPECTION_CONFIG_ADMIN"],
  "/video-operations": ["TENANT_ADMIN"],
  "/baseline-policy": ["TENANT_ADMIN", "INSPECTION_CONFIG_ADMIN"],
  "/governance": ["TENANT_ADMIN", "GOVERNANCE_ADMIN"],
  "/prompts": ["TENANT_ADMIN", "INSPECTION_CONFIG_ADMIN"],
};

/** Checks the role boundary for one Admin route before its protected query runs. */
export function hasAdminRouteAccess(pathname: string, roles: string[]): boolean {
  const requiredRoles = adminPageRoles[pathname];
  return requiredRoles ? roles.some((role) => requiredRoles.includes(role)) : roles.some((role) => ["TENANT_ADMIN", "ORGANIZATION_ADMIN", "ACCESS_ADMIN", "PARTICIPATION_ADMIN", "INSPECTION_CONFIG_ADMIN", "GOVERNANCE_ADMIN", "AUDITOR"].includes(role));
}

/** Reports whether the current Admin session may open the global analysis prompt. */
export function hasAnalysisPromptAccess(current = identity): boolean {
  return Boolean(
    current
      && hasAdminAccess(current)
      && current.roles.some((role) => role === "TENANT_ADMIN" || role === "INSPECTION_CONFIG_ADMIN"),
  );
}

export function hasDashboardAccess(current = identity): boolean {
  return Boolean(current?.roles.includes("TENANT_ADMIN") && current.entitlements.includes("DASHBOARD"));
}
