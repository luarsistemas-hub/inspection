import { clearSession, getAccessToken } from "@/auth/session";

export type GraphQLFailure = { message: string; code?: string; field?: string };
type Response<T> = { data?: T; errors?: Array<{ message: string; extensions?: { code?: string; field?: string } }> };
const endpoint = process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql";

export async function graphql<T>(query: string, variables?: Record<string, unknown>, idempotencyKey?: string): Promise<T> {
  const token = getAccessToken();
  if (!token) throw { message: "Autenticação necessária", code: "UNAUTHENTICATED" } satisfies GraphQLFailure;
  const response = await fetch(endpoint, { method: "POST", credentials: "omit", headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}`, ...(idempotencyKey ? { "Idempotency-Key": idempotencyKey } : {}) }, body: JSON.stringify({ query, variables }) });
  let body: Response<T> = {};
  const raw = await response.text();
  if (raw.trimStart().startsWith("{")) { try { body = JSON.parse(raw) as Response<T>; } catch { body = {}; } }
  if (response.status === 401 || response.status === 403) {
    clearSession();
    throw { message: response.status === 401 ? "Sua sessão expirou. Entre novamente." : "Você não tem permissão para esta operação.", code: response.status === 401 ? "UNAUTHENTICATED" : "FORBIDDEN" } satisfies GraphQLFailure;
  }
  const error = body.errors?.[0];
  if (!response.ok || error) { if (["FORBIDDEN", "UNAUTHENTICATED", "TENANT_INACTIVE"].includes(error?.extensions?.code ?? "")) clearSession(); throw { message: error?.message ?? "Não foi possível concluir a solicitação", code: error?.extensions?.code, field: error?.extensions?.field } satisfies GraphQLFailure; }
  if (!body.data) throw { message: "Resposta vazia da API" } satisfies GraphQLFailure;
  return body.data;
}
