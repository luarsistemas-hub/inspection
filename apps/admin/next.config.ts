import type { NextConfig } from "next";

function originOf(value: string | undefined, fallback: string): string {
  try { return new URL(value ?? fallback).origin; } catch { return new URL(fallback).origin; }
}

const apiOrigin = originOf(process.env.NEXT_PUBLIC_INSPECTION_API_URL, "http://localhost:8080/graphql");
const oidcOrigin = originOf(process.env.NEXT_PUBLIC_OIDC_TOKEN_URL, "http://localhost:8081/realms/inspection/protocol/openid-connect/token");

const config: NextConfig = {
  poweredByHeader: false,
  async headers() {
    return [{ source: "/(.*)", headers: [
      { key: "X-Content-Type-Options", value: "nosniff" },
      { key: "X-Frame-Options", value: "DENY" },
      { key: "Referrer-Policy", value: "same-origin" },
      { key: "Permissions-Policy", value: "geolocation=(), camera=()" },
      { key: "Content-Security-Policy", value: `default-src 'self'; connect-src 'self' ${apiOrigin} ${oidcOrigin}; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; frame-ancestors 'none'` }
    ] }];
  }
};

export default config;
