const sessionCookie = "inspection_video_session";
const forwardedHeaders = ["accept-ranges", "content-range", "content-length", "content-type"] as const;
const allowedStatuses = new Set([200, 206, 401, 404, 409, 410, 416, 429, 503]);

type RouteContext = { params: Promise<{ id: string }> };

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

async function proxy(request: Request, context: RouteContext, method: "GET" | "HEAD"): Promise<Response> {
  const serializedSession = cookieValue(request, sessionCookie);
  let session: { accessToken: string; membershipId?: string } | undefined;
  if (serializedSession) {
    try {
      const parsed = JSON.parse(serializedSession) as { accessToken?: unknown; membershipId?: unknown };
      if (typeof parsed.accessToken === "string" && parsed.accessToken.length > 0) {
        session = {
          accessToken: parsed.accessToken,
          ...(typeof parsed.membershipId === "string" ? { membershipId: parsed.membershipId } : {}),
        };
      }
    } catch { /* Ignore malformed session cookies. */ }
  }
  if (!session) return new Response(null, { status: 401, headers: { "Cache-Control": "private, no-store" } });

  const { id } = await context.params;
  const snapshotId = new URL(request.url).searchParams.get("snapshotId") ?? "";
  const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;
  if (!uuid.test(id) || !uuid.test(snapshotId)) {
    return new Response(null, { status: 404, headers: { "Cache-Control": "private, no-store" } });
  }

  const headers: Record<string, string> = { Authorization: `Bearer ${session.accessToken}` };
  const membershipId = session.membershipId;
  if (membershipId && /^[0-9a-f-]{36}$/i.test(membershipId)) headers["X-Inspection-Membership-ID"] = membershipId;
  const range = request.headers.get("range");
  if (range) headers.Range = range;

  let upstream: Response;
  try {
    const url = new URL(`/media/videos/${encodeURIComponent(id)}/content`, apiOrigin());
    url.searchParams.set("snapshotId", snapshotId);
    upstream = await fetch(url, { method, headers, cache: "no-store", redirect: "manual", signal: request.signal });
  } catch {
    return new Response(null, { status: 503, headers: { "Cache-Control": "private, no-store" } });
  }

  const responseHeaders = new Headers({ "Cache-Control": "private, no-store" });
  for (const name of forwardedHeaders) {
    const value = upstream.headers.get(name);
    if (value !== null) responseHeaders.set(name, value);
  }
  const status = allowedStatuses.has(upstream.status) ? upstream.status : 503;
  return new Response(status === 503 && !allowedStatuses.has(upstream.status) ? null : method === "HEAD" ? null : upstream.body, {
    status,
    headers: responseHeaders,
  });
}

export async function GET(request: Request, context: RouteContext): Promise<Response> {
  return proxy(request, context, "GET");
}

export async function HEAD(request: Request, context: RouteContext): Promise<Response> {
  return proxy(request, context, "HEAD");
}
