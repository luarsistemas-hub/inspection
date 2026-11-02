import type { TypedDocumentNode } from "@graphql-typed-document-node/core";
import { getOperationAST, print } from "graphql";
import { accessTokenForRequest, clearSession, getMembershipId, getProtectedStateGeneration, getSessionVersion } from "@/auth/session";

export type GraphQLFailure = { message: string; code?: string; field?: string; correlationId?: string };
type GraphQLErrorResponse = { message: string; extensions?: { code?: string; field?: string; correlationId?: string; [key: string]: unknown } };
type Response<T> = { data?: T; errors?: GraphQLErrorResponse[] };
const endpoint = process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql";
const genericFailure = "Não foi possível concluir a solicitação. Tente novamente.";
const transportMessages: Record<string, string> = {
  NOT_FOUND: "Registro não encontrado ou sem acesso.", CONFLICT: "Os dados mudaram. Atualize e tente novamente.", VERSION_CONFLICT: "Os dados mudaram. Atualize e tente novamente.",
  INVALID_INPUT: "Revise os dados informados e tente novamente.", INVALID_STATE: "Revise os dados informados e tente novamente.",
  ALREADY_RESOLVED: "Esta solicitação já foi resolvida.", DEADLINE_PASSED: "O prazo para esta ação terminou.", LIMIT_REACHED: "O limite permitido foi atingido.",
  RATE_LIMITED: "Muitas tentativas foram realizadas. Aguarde e tente novamente.", MEMBERSHIP_REQUIRED: "Selecione um contexto de acesso antes de continuar.",
  SESSION_EXPIRED: "Sua sessão expirou. Entre novamente.", UNAUTHENTICATED: "Sua sessão expirou. Entre novamente.", FORBIDDEN: "Você não tem permissão para esta operação.",
  CONTEXT_CHANGED: "O contexto de acesso mudou. Os dados anteriores foram descartados.",
};

/** Reports whether a failure only means that the response belonged to a replaced access context. */
export function isStaleContextFailure(failure: unknown): boolean {
  return typeof failure === "object" && failure !== null && (failure as GraphQLFailure).code === "CONTEXT_CHANGED";
}

/** Returns the stable Portuguese message for a transport error code. */
export function transportFailureMessage(code?: string): string { return code ? transportMessages[code] ?? genericFailure : genericFailure; }

export function graphQLFailureText(failure: unknown): string {
  if (typeof failure !== "object" || failure === null) return genericFailure;
  if (failure instanceof Error && "isUserError" in failure && (failure as Error & { isUserError?: boolean }).isUserError) return failure.message;
  const value = failure as GraphQLFailure;
  const message = transportFailureMessage(value.code);
  return value.correlationId && value.correlationId !== "unavailable" ? `${message} Código de suporte: ${value.correlationId}` : message;
}

function logOperationErrors<T, V>(query: TypedDocumentNode<T, V>, errors: GraphQLErrorResponse[]): void {
  const operation = getOperationAST(query);
  const operationLabel = operation ? `${operation.operation} ${operation.name?.value ?? "(anonymous)"}` : "unknown operation";
  console.error(`[GraphQL] ${operationLabel} failed`, { errors });
}

export async function graphql<T, V>(query: TypedDocumentNode<T, V>, variables: V, idempotencyKey?: string): Promise<T> {
  return execute(query, variables, idempotencyKey);
}

async function execute<T, V>(query: TypedDocumentNode<T, V>, variables: V, idempotencyKey?: string, retried = false): Promise<T> {
  const sessionVersion = getSessionVersion();
  const protectedGeneration = getProtectedStateGeneration();
  const token = await accessTokenForRequest();
  if (sessionVersion !== getSessionVersion()) throw { message: "Autenticação necessária", code: "UNAUTHENTICATED" } satisfies GraphQLFailure;
  if (!token) throw { message: "Autenticação necessária", code: "UNAUTHENTICATED" } satisfies GraphQLFailure;
  const membershipId = getMembershipId();
  let response: globalThis.Response;
  try { response = await fetch(endpoint, { method: "POST", credentials: "omit", headers: { "Content-Type": "application/json", Authorization: `Bearer ${token}`, ...(membershipId ? { "X-Inspection-Membership-ID": membershipId } : {}), ...(idempotencyKey ? { "Idempotency-Key": idempotencyKey } : {}) }, body: JSON.stringify({ query: print(query), variables }) }); }
  catch { throw { message: genericFailure, code: "NETWORK_ERROR" } satisfies GraphQLFailure; }
  let body: Response<T> = {};
  const raw = await response.text();
  if (sessionVersion !== getSessionVersion()) throw { message: "Autenticação necessária", code: "UNAUTHENTICATED" } satisfies GraphQLFailure;
  // A membership switch keeps the session but replaces every protected projection.
  if (protectedGeneration !== getProtectedStateGeneration()) throw { message: transportMessages.CONTEXT_CHANGED, code: "CONTEXT_CHANGED" } satisfies GraphQLFailure;
  if (raw.trimStart().startsWith("{")) { try { body = JSON.parse(raw) as Response<T>; } catch { body = {}; } }
  if (body.errors?.length) logOperationErrors(query, body.errors);
  const error = body.errors?.[0];
  if (!retried && (response.status === 401 || error?.extensions?.code === "UNAUTHENTICATED")) {
    const renewed = await accessTokenForRequest(token);
    if (renewed && renewed !== token) return execute(query, variables, idempotencyKey, true);
  }
  if (response.status === 401 || response.status === 403) {
    clearSession();
    throw { message: response.status === 401 ? "Sua sessão expirou. Entre novamente." : "Você não tem permissão para esta operação.", code: response.status === 401 ? "UNAUTHENTICATED" : "FORBIDDEN" } satisfies GraphQLFailure;
  }
  if (!response.ok || error) { if (["FORBIDDEN", "UNAUTHENTICATED", "TENANT_INACTIVE"].includes(error?.extensions?.code ?? "")) clearSession(); const code = error?.extensions?.code; throw { message: transportFailureMessage(code), code, field: error?.extensions?.field, correlationId: error?.extensions?.correlationId } satisfies GraphQLFailure; }
  if (!body.data) throw { message: "Resposta vazia da API" } satisfies GraphQLFailure;
  return body.data;
}
