import type { NextConfig } from "next";

const originOf = (value: string | undefined, fallback: string): string => {
  try { return new URL(value ?? fallback).origin; } catch { return new URL(fallback).origin; }
};

const apiOrigin = originOf(process.env.NEXT_PUBLIC_INSPECTION_API_URL, "http://localhost:8080/graphql");
const storageOrigin = originOf(process.env.NEXT_PUBLIC_STORAGE_URL, "http://localhost:9002");

const config: NextConfig = {
  output: "standalone",
  poweredByHeader: false,
  async headers() {
    return [{ source: "/(.*)", headers: [
      { key: "X-Content-Type-Options", value: "nosniff" },
      { key: "X-Frame-Options", value: "DENY" },
      { key: "Referrer-Policy", value: "same-origin" },
      { key: "Permissions-Policy", value: "geolocation=(self), camera=(self)" },
      { key: "Content-Security-Policy", value: `default-src 'self'; connect-src 'self' ${apiOrigin} ${storageOrigin}; img-src 'self' blob: data:; media-src 'self' blob:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; frame-ancestors 'none'` }
    ] }];
  }
};

export default config;
