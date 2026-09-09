import { clearProtectedContext, clearSession, getAccessToken, restoreMembershipContext } from "@/auth/session";
import { print } from "graphql";
import type { TypedDocumentNode } from "@graphql-typed-document-node/core";

export type GraphQLFailure = { message: string; code?: string; field?: string };
type Response<T> = { data?: T; errors?: Array<{ message: string; extensions?: { code?: string; field?: string } }> };
const endpoint = process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql";

async function execute<T, V>(document: TypedDocumentNode<unknown, never>, variables: V | undefined, signal: AbortSignal | undefined, requiresMembership: boolean): Promise<T> {
  const token = getAccessToken();
  if (!token) throw { message: "Autenticação necessária", code: "UNAUTHENTICATED" } satisfies GraphQLFailure;
  const membershipId = requiresMembership ? restoreMembershipContext()?.membershipId : undefined;
  if (requiresMembership && !membershipId) throw { message: "Selecione um contexto de acesso antes de carregar dados.", code: "MEMBERSHIP_REQUIRED" } satisfies GraphQLFailure;
  const headers: Record<string, string> = { "Content-Type": "application/json", Authorization: `Bearer ${token}` };
  if (requiresMembership && membershipId) headers["X-Inspection-Membership-ID"] = membershipId;
  const response = await fetch(endpoint, { method: "POST", credentials: "omit", signal, headers, body: JSON.stringify({ query: print(document), variables }) });
  let body: Response<T> = {};
  const raw = await response.text();
  if (raw.trimStart().startsWith("{")) { try { body = JSON.parse(raw) as Response<T>; } catch { body = {}; } }
  if (response.status === 401 || response.status === 403) {
    clearProtectedContext(); clearSession();
    throw { message: response.status === 401 ? "Sua sessão expirou. Entre novamente." : "Você não tem permissão para esta operação.", code: response.status === 401 ? "UNAUTHENTICATED" : "FORBIDDEN" } satisfies GraphQLFailure;
  }
  const error = body.errors?.[0];
  if (!response.ok || error) {
    if (["FORBIDDEN", "UNAUTHENTICATED", "TENANT_INACTIVE"].includes(error?.extensions?.code ?? "")) { clearProtectedContext(); clearSession(); }
    throw { message: error?.message ?? "Não foi possível concluir a solicitação", code: error?.extensions?.code, field: error?.extensions?.field } satisfies GraphQLFailure;
  }
  if (!body.data) throw { message: "Resposta vazia da API" } satisfies GraphQLFailure;
  return body.data;
}

export function graphqlIdentity<T, V = unknown>(document: TypedDocumentNode<unknown, never>, variables?: V, signal?: AbortSignal): Promise<T> {
  return execute<T, V>(document, variables, signal, false);
}

export function graphql<T, V = unknown>(document: TypedDocumentNode<unknown, never>, variables?: V, signal?: AbortSignal): Promise<T> {
  return execute<T, V>(document, variables, signal, true);
}
