export const adminRoutes = ["/auth/callback", "/overview", "/tenants/[tenantId]", "/organization", "/access", "/catalogs", "/assets", "/governance", "/audit"] as const;

export function legacyRedirects(dashboardOrigin: string, captureOrigin: string) {
  return [
    { source: "/dashboard", destination: `${dashboardOrigin}/`, permanent: false },
    { source: "/dashboard/:path*", destination: `${dashboardOrigin}/:path*`, permanent: false },
    { source: "/capture", destination: `${captureOrigin}/`, permanent: false },
    { source: "/capture/:path*", destination: `${captureOrigin}/capture/:path*`, permanent: false },
  ];
}
