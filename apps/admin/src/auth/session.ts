"use client";

export type AdminIdentity = { tenantId: string; tenantName: string; entitlements: string[]; roles: string[] };

let token: string | undefined;
let identity: AdminIdentity | undefined;

export function setSession(nextToken: string, nextIdentity?: AdminIdentity): void { token = nextToken; identity = nextIdentity; }
export function setIdentity(nextIdentity: AdminIdentity): void { identity = nextIdentity; }
export function getAccessToken(): string | undefined { return token; }
export function getIdentity(): AdminIdentity | undefined { return identity; }
export function clearSession(): void { token = undefined; identity = undefined; }
export function hasAdminAccess(current = identity): boolean {
  return Boolean(current?.entitlements.includes("ADMIN") && current.roles.includes("TENANT_ADMIN"));
}
