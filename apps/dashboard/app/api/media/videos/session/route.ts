const sessionCookie = "inspection_video_session";
const cookieLifetimeSeconds = 10 * 60;

function sameOrigin(request: Request): boolean {
  const origin = request.headers.get("origin");
  const url = new URL(request.url);
  // Standalone Next.js uses its bind address in request.url inside Docker.
  const host = request.headers.get("host") ?? url.host;
  return origin !== null && origin === `${url.protocol}//${host}`;
}

function cookieOptions(request: Request, name: string, value: string, maxAge: number): string {
  const secure = new URL(request.url).protocol === "https:" ? "; Secure" : "";
  return `${name}=${encodeURIComponent(value)}; Path=/api/media/videos/; Max-Age=${maxAge}; HttpOnly; SameSite=Strict${secure}`;
}

export async function POST(request: Request): Promise<Response> {
  if (!sameOrigin(request)) return new Response(null, { status: 403, headers: { "Cache-Control": "no-store" } });
  const authorization = request.headers.get("authorization") ?? "";
  const match = /^Bearer\s+(\S+)$/i.exec(authorization);
  if (!match) return new Response(null, { status: 401, headers: { "Cache-Control": "no-store" } });

  const membershipId = request.headers.get("x-inspection-membership-id") ?? "";
  if (membershipId && !/^[0-9a-f-]{36}$/i.test(membershipId)) {
    return new Response(null, { status: 400, headers: { "Cache-Control": "no-store" } });
  }

  const sessionValue = JSON.stringify({ accessToken: match[1], membershipId });
  if (encodeURIComponent(sessionValue).length > 3500) return new Response(null, { status: 400, headers: { "Cache-Control": "no-store" } });
  const headers = new Headers({ "Cache-Control": "no-store" });
  headers.append("Set-Cookie", cookieOptions(request, sessionCookie, sessionValue, cookieLifetimeSeconds));
  return new Response(null, { status: 204, headers });
}

export async function DELETE(request: Request): Promise<Response> {
  if (!sameOrigin(request)) return new Response(null, { status: 403, headers: { "Cache-Control": "no-store" } });
  const headers = new Headers({ "Cache-Control": "no-store" });
  headers.append("Set-Cookie", cookieOptions(request, sessionCookie, "", 0));
  return new Response(null, { status: 204, headers });
}
