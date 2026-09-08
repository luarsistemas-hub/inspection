import { clearSession, getAccessToken } from "@/auth/session";

export type GraphQLFailure = { message: string; code?: string; field?: string };
type Response<T> = { data?: T; errors?: Array<{ message: string; extensions?: { code?: string; field?: string } }> };
const endpoint = process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql";

export async function graphql<T>(query: string, variables?: Record<string, unknown>): Promise<T> {
  const token = getAccessToken();
  if (!token) throw { message: "Autenticação necessária", code: "UNAUTHENTICATED" } satisfies GraphQLFailure;
  const response = await fetch(endpoint, { method: "POST", credentials: "omit", headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}` }, body: JSON.stringify({ query, variables }) });
  const body = await response.json() as Response<T>;
  const error = body.errors?.[0];
  if (!response.ok || error) {
    if (error?.extensions?.code === "FORBIDDEN" || error?.extensions?.code === "TENANT_INACTIVE") clearSession();
    throw { message: error?.message ?? "Não foi possível concluir a solicitação", code: error?.extensions?.code, field: error?.extensions?.field } satisfies GraphQLFailure;
  }
  if (!body.data) throw { message: "Resposta vazia da API" } satisfies GraphQLFailure;
  return body.data;
}
