"use client";

export type DashboardIdentity = { tenantId: string; tenantName: string; tenantStatus: string; entitlements: string[]; roles: string[]; scopes: Array<{ kind: string; resourceId: string }> };
const membershipStorageKey = "inspection.dashboard.membership";
function readStoredMembership(): string | undefined {
  if (typeof window === "undefined") return undefined;
  return window.sessionStorage.getItem(membershipStorageKey) ?? undefined;
}
let token: string | undefined;
let identity: DashboardIdentity | undefined;
let membershipId: string | undefined = readStoredMembership();
let protectedStateGeneration = 0;
export function setSession(nextToken: string, nextIdentity?: DashboardIdentity): void { token = nextToken; identity = nextIdentity; membershipId = readStoredMembership(); protectedStateGeneration += 1; }
export function setIdentity(nextIdentity: DashboardIdentity): void { identity = nextIdentity; }
export function getAccessToken(): string | undefined { return token; }
export function getIdentity(): DashboardIdentity | undefined { return identity; }
export function getMembershipId(): string | undefined { return membershipId; }
export function getProtectedStateGeneration(): number { return protectedStateGeneration; }
export function selectMembership(nextMembershipId: string): void { if (membershipId !== nextMembershipId) { membershipId = nextMembershipId; window.sessionStorage.setItem(membershipStorageKey, nextMembershipId); identity = undefined; protectedStateGeneration += 1; } }
export function clearSession(): void { token = undefined; identity = undefined; membershipId = undefined; window.sessionStorage.removeItem(membershipStorageKey); protectedStateGeneration += 1; }
export function hasDashboardAccess(current = identity): boolean { return Boolean(current?.tenantStatus === "ACTIVE" && current.entitlements.includes("DASHBOARD") && current.roles.length); }
export function isSuperAdmin(current: Pick<DashboardIdentity, "entitlements" | "roles"> | undefined = identity): boolean {
  return Boolean(
    current?.roles.includes("TENANT_ADMIN")
      && current.entitlements.includes("ADMIN")
      && current.entitlements.includes("DASHBOARD"),
  );
}
