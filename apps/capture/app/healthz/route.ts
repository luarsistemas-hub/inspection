export function GET(): Response {
  return new Response("ready", { status: 200, headers: { "Cache-Control": "no-store" } });
}
