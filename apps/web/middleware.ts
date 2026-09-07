import { NextRequest, NextResponse } from "next/server";

function originOf(value: string | undefined): string | undefined {
  if (!value) return undefined;
  try { return new URL(value).origin; } catch { return undefined; }
}

// Production responses use a per-request nonce so dashboard and capture code
// do not need unsafe-inline scripts. Local development keeps Next's dev tools
// working; acceptance and production use the strict policy below.
export function middleware(request: NextRequest): NextResponse {
  if (process.env.NODE_ENV !== "production") return NextResponse.next();
  const nonce = crypto.randomUUID().replaceAll("-", "");
  const api = originOf(process.env.NEXT_PUBLIC_INSPECTION_API_URL);
  const oidc = originOf(process.env.NEXT_PUBLIC_OIDC_AUTHORIZE_URL);
  const storage = originOf(process.env.NEXT_PUBLIC_STORAGE_URL);
  const connectSources = ["'self'", api, oidc, storage].filter(Boolean).join(" ");
  const csp = [
    "default-src 'self'",
    `script-src 'self' 'nonce-${nonce}'`,
    "style-src 'self'",
    "img-src 'self' blob: data:",
    "font-src 'self'",
    `connect-src ${connectSources}`,
    "object-src 'none'",
    "base-uri 'self'",
    "frame-ancestors 'none'",
    "form-action 'self'"
  ].join("; ");
  const requestHeaders = new Headers(request.headers);
  requestHeaders.set("x-nonce", nonce);
  const response = NextResponse.next({ request: { headers: requestHeaders } });
  response.headers.set("Content-Security-Policy", csp);
  response.headers.set("Referrer-Policy", "same-origin");
  response.headers.set("X-Content-Type-Options", "nosniff");
  response.headers.set("X-Frame-Options", "DENY");
  return response;
}

export const config = { matcher: ["/((?!_next/static|_next/image|favicon.ico).*)"] };
