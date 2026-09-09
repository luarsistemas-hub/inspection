"use client";

export type AdminIdentity = { tenantId: string; tenantName: string; entitlements: string[]; roles: string[]; membershipId?: string; scope?: string };

let token: string | undefined;
let identity: AdminIdentity | undefined;

export function setSession(nextToken: string, nextIdentity?: AdminIdentity): void { token = nextToken; identity = nextIdentity; }
export function setIdentity(nextIdentity: AdminIdentity): void { identity = nextIdentity; }
export function getAccessToken(): string | undefined { return token; }
export function getIdentity(): AdminIdentity | undefined { return identity; }
export function clearSession(): void { token = undefined; identity = undefined; }
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

export function hasDashboardAccess(current = identity): boolean {
  return Boolean(current?.roles.includes("TENANT_ADMIN") && current.entitlements.includes("DASHBOARD"));
}
