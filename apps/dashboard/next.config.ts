import type { NextConfig } from "next";

const apiOrigin = (() => { try { return new URL(process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql").origin; } catch { return "http://localhost:8080"; } })();
const oidcOrigin = (() => { try { return new URL(process.env.NEXT_PUBLIC_OIDC_TOKEN_URL ?? "http://localhost:8081/realms/inspection/protocol/openid-connect/token").origin; } catch { return "http://localhost:8081"; } })();
const nextConfig: NextConfig = {
  output: "standalone",
  poweredByHeader: false,
  async headers() { return [{ source: "/(.*)", headers: [
    { key: "X-Content-Type-Options", value: "nosniff" },
    { key: "X-Frame-Options", value: "DENY" },
    { key: "Referrer-Policy", value: "same-origin" },
    { key: "Permissions-Policy", value: "geolocation=(), camera=()" },
    { key: "Content-Security-Policy", value: `default-src 'self'; connect-src 'self' ${apiOrigin} ${oidcOrigin}; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'` }
  ] }]; }
};
export default nextConfig;
