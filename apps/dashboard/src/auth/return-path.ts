const owned = ["/tenants/", "/inspections", "/projects", "/triage", "/portfolio", "/reports", "/notifications"];

export function safeDashboardPath(value: string | undefined, fallback = "/tenants/current"): string {
  if (!value || !value.startsWith("/") || value.startsWith("//")) return fallback;
  return owned.some((prefix) => value === prefix.slice(0, -1) || value.startsWith(prefix)) ? value : fallback;
}
