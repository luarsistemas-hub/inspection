const ownedPrefixes = ["/overview", "/tenants/", "/organization", "/access", "/catalogs", "/assets", "/governance", "/audit"];

export function safeAdminPath(value: string | null | undefined, fallback = "/organization"): string {
  if (!value || !value.startsWith("/") || value.startsWith("//") || value.includes("\\")) return fallback;
  try {
    const path = new URL(value, "http://admin.local");
    return ownedPrefixes.some((prefix) => path.pathname === prefix.slice(0, -1) || path.pathname.startsWith(prefix))
      ? `${path.pathname}${path.search}${path.hash}` : fallback;
  } catch { return fallback; }
}
