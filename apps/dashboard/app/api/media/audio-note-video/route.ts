const sessionCookie = "inspection_video_session";
const forwardedHeaders = ["accept-ranges", "content-range", "content-length", "content-type"] as const;
const allowedStatuses = new Set([200, 206, 401, 404, 409, 410, 416, 429, 503]);

function cookieValue(request: Request, name: string): string | undefined {
  const prefix = `${name}=`;
  const pair = (request.headers.get("cookie") ?? "").split(";").map((part) => part.trim()).find((part) => part.startsWith(prefix));
  if (!pair) return undefined;
  try { return decodeURIComponent(pair.slice(prefix.length)); } catch { return undefined; }
}

function apiOrigin(): string {
  const configured = process.env.INSPECTION_INTERNAL_API_URL || process.env.NEXT_PUBLIC_INSPECTION_API_URL || "http://localhost:8080/graphql";
  return new URL(configured).origin;
}

/** Proxies an authorized audio-note video route while preserving byte-range playback. */
export async function GET(request: Request): Promise<Response> {
  const serializedSession = cookieValue(request, sessionCookie);
  let session: { accessToken: string; membershipId?: string } | undefined;
  if (serializedSession) {
    try {
      const parsed = JSON.parse(serializedSession) as { accessToken?: unknown; membershipId?: unknown };
      if (typeof parsed.accessToken === "string" && parsed.accessToken.length > 0) {
        session = { accessToken: parsed.accessToken, ...(typeof parsed.membershipId === "string" ? { membershipId: parsed.membershipId } : {}) };
      }
    } catch { /* Ignore malformed session cookies. */ }
  }
  if (!session) return new Response(null, { status: 401, headers: { "Cache-Control": "private, no-store" } });

  const source = new URL(request.url).searchParams.get("path") ?? "";
  let target: URL;
  try { target = new URL(source, apiOrigin()); } catch { return new Response(null, { status: 404, headers: { "Cache-Control": "private, no-store" } }); }
  if (target.origin !== apiOrigin() || !/^\/media\/videos\/[A-Za-z0-9_-]+\/content$/.test(target.pathname)) {
    return new Response(null, { status: 404, headers: { "Cache-Control": "private, no-store" } });
  }

  const headers: Record<string, string> = { Authorization: `Bearer ${session.accessToken}` };
  if (session.membershipId && /^[0-9a-f-]{36}$/i.test(session.membershipId)) headers["X-Inspection-Membership-ID"] = session.membershipId;
  const range = request.headers.get("range");
  if (range) headers.Range = range;
  let upstream: Response;
  try { upstream = await fetch(target, { headers, cache: "no-store", redirect: "manual", signal: request.signal }); }
  catch { return new Response(null, { status: 503, headers: { "Cache-Control": "private, no-store" } }); }

  const responseHeaders = new Headers({ "Cache-Control": "private, no-store" });
  for (const name of forwardedHeaders) {
    const value = upstream.headers.get(name);
    if (value !== null) responseHeaders.set(name, value);
  }
  const status = allowedStatuses.has(upstream.status) ? upstream.status : 503;
  return new Response(status === 503 && !allowedStatuses.has(upstream.status) ? null : upstream.body, { status, headers: responseHeaders });
}
