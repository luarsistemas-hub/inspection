import type { NextConfig } from "next";

const originOf = (value: string | undefined, fallback: string): string => {
  try { return new URL(value ?? fallback).origin; } catch { return new URL(fallback).origin; }
};

const inspectionOrigin = originOf(process.env.NEXT_PUBLIC_INSPECTION_API_URL, "http://localhost:8080/graphql");
const storageOrigin = originOf(process.env.NEXT_PUBLIC_STORAGE_URL, "http://localhost:9002");

const config: NextConfig = {
  poweredByHeader: false,
  async headers() {
    // Next's App Router streams a small inline bootstrap/RSC payload. Keep the
    // inline allowance explicit; eval remains development-only.
    const devScript = process.env.NODE_ENV === "development" ? " 'unsafe-eval'" : "";
    return [{
      source: "/(.*)",
      headers: [
        { key: "X-Content-Type-Options", value: "nosniff" },
        { key: "Referrer-Policy", value: "same-origin" },
        { key: "Permissions-Policy", value: "geolocation=(self), camera=(self)" },
        { key: "Content-Security-Policy", value: `default-src 'self'; connect-src 'self' ${inspectionOrigin} ${storageOrigin}; img-src 'self' blob: data:; media-src 'self' blob:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'${devScript}` }
      ]
    }];
  }
};

export default config;
