import type { TypedDocumentNode } from "@graphql-typed-document-node/core";
import { getOperationAST, print } from "graphql";
import { clearSession, getAccessToken, getMembershipId } from "@/auth/session";

export type GraphQLFailure = { message: string; code?: string; field?: string };
type GraphQLErrorResponse = { message: string; extensions?: { code?: string; field?: string; correlationId?: string; [key: string]: unknown } };
type Response<T> = { data?: T; errors?: GraphQLErrorResponse[] };
const endpoint = process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql";

function logOperationErrors<T, V>(query: TypedDocumentNode<T, V>, errors: GraphQLErrorResponse[]): void {
  const operation = getOperationAST(query);
  const operationLabel = operation ? `${operation.operation} ${operation.name?.value ?? "(anonymous)"}` : "unknown operation";
  console.error(`[GraphQL] ${operationLabel} failed`, { errors });
}

export async function graphql<T, V>(query: TypedDocumentNode<T, V>, variables: V, idempotencyKey?: string): Promise<T> {
  const token = getAccessToken();
  if (!token) throw { message: "Autenticação necessária", code: "UNAUTHENTICATED" } satisfies GraphQLFailure;
  const membershipId = getMembershipId();
  const response = await fetch(endpoint, { method: "POST", credentials: "omit", headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}`, ...(membershipId ? { "X-Inspection-Membership-ID": membershipId } : {}), ...(idempotencyKey ? { "Idempotency-Key": idempotencyKey } : {}) }, body: JSON.stringify({ query: print(query), variables }) });
  let body: Response<T> = {};
  const raw = await response.text();
  if (raw.trimStart().startsWith("{")) { try { body = JSON.parse(raw) as Response<T>; } catch { body = {}; } }
  if (body.errors?.length) logOperationErrors(query, body.errors);
  if (response.status === 401 || response.status === 403) {
    clearSession();
    throw { message: response.status === 401 ? "Sua sessão expirou. Entre novamente." : "Você não tem permissão para esta operação.", code: response.status === 401 ? "UNAUTHENTICATED" : "FORBIDDEN" } satisfies GraphQLFailure;
  }
  const error = body.errors?.[0];
  if (!response.ok || error) { if (["FORBIDDEN", "UNAUTHENTICATED", "TENANT_INACTIVE"].includes(error?.extensions?.code ?? "")) clearSession(); throw { message: error?.message ?? "Não foi possível concluir a solicitação", code: error?.extensions?.code, field: error?.extensions?.field } satisfies GraphQLFailure; }
  if (!body.data) throw { message: "Resposta vazia da API" } satisfies GraphQLFailure;
  return body.data;
}
