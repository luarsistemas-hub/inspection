import { accessTokenForRequest, clearProtectedContext, clearSession, getSessionVersion, restoreMembershipContext } from "@/auth/session";
import { getOperationAST, print } from "graphql";
import type { TypedDocumentNode } from "@graphql-typed-document-node/core";

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
};

/** Returns the stable Portuguese message for a transport error code. */
export function transportFailureMessage(code?: string): string { return code ? transportMessages[code] ?? genericFailure : genericFailure; }

/** Marks a business validation message returned in GraphQL userErrors. */
export class AdminUserError extends Error {
  readonly code: string;
  constructor(code: string, message: string) { super(message); this.name = "AdminUserError"; this.code = code; }
}

/** Marks a Portuguese message created locally by the Admin interface. */
export class AdminLocalError extends Error { constructor(message: string) { super(message); this.name = "AdminLocalError"; } }

/** Throws the first business validation returned by a mutation. */
export function throwAdminUserErrors(errors: Array<{ code: string; message: string }>): void {
  if (errors.length) throw new AdminUserError(errors[0].code, errors.map((item) => item.message).join(" · "));
}

/** Converts transport and local failures into stable Portuguese text for the Admin UI. */
export function graphQLFailureText(failure: unknown): string {
  if (failure instanceof AdminUserError || failure instanceof AdminLocalError) return failure.message;
  if (typeof failure !== "object" || failure === null) return genericFailure;
  const value = failure as GraphQLFailure;
  const message = transportFailureMessage(value.code);
  return value.correlationId && value.correlationId !== "unavailable" ? `${message} Código de suporte: ${value.correlationId}` : message;
}

function logOperationErrors(document: TypedDocumentNode<unknown, never>, errors: GraphQLErrorResponse[]): void {
  const operation = getOperationAST(document);
  const operationLabel = operation ? `${operation.operation} ${operation.name?.value ?? "(anonymous)"}` : "unknown operation";
  console.error(`[GraphQL] ${operationLabel} failed`, { errors });
}

async function execute<T, V>(document: TypedDocumentNode<unknown, never>, variables: V | undefined, signal: AbortSignal | undefined, requiresMembership: boolean, preserveForbidden = false, retried = false): Promise<T> {
  const sessionVersion = getSessionVersion();
  const token = await accessTokenForRequest();
  if (sessionVersion !== getSessionVersion()) throw { message: "Autenticação necessária", code: "UNAUTHENTICATED" } satisfies GraphQLFailure;
  if (!token) throw { message: "Autenticação necessária", code: "UNAUTHENTICATED" } satisfies GraphQLFailure;
  const membershipId = requiresMembership ? restoreMembershipContext()?.membershipId : undefined;
  if (requiresMembership && !membershipId) throw { message: "Selecione um contexto de acesso antes de carregar dados.", code: "MEMBERSHIP_REQUIRED" } satisfies GraphQLFailure;
  const headers: Record<string, string> = { "Content-Type": "application/json", Authorization: `Bearer ${token}` };
  if (requiresMembership && membershipId) headers["X-Inspection-Membership-ID"] = membershipId;
  let response: globalThis.Response;
  try { response = await fetch(endpoint, { method: "POST", credentials: "omit", signal, headers, body: JSON.stringify({ query: print(document), variables }) }); }
  catch { throw { message: genericFailure, code: "NETWORK_ERROR" } satisfies GraphQLFailure; }
  let body: Response<T> = {};
  const raw = await response.text();
  if (sessionVersion !== getSessionVersion()) throw { message: "Autenticação necessária", code: "UNAUTHENTICATED" } satisfies GraphQLFailure;
  if (raw.trimStart().startsWith("{")) { try { body = JSON.parse(raw) as Response<T>; } catch { body = {}; } }
  if (body.errors?.length) logOperationErrors(document, body.errors);
  const error = body.errors?.[0];
  if (!retried && (response.status === 401 || error?.extensions?.code === "UNAUTHENTICATED")) {
    const renewed = await accessTokenForRequest(token);
    if (renewed && renewed !== token) return execute(document, variables, signal, requiresMembership, preserveForbidden, true);
  }
  if (response.status === 401 || response.status === 403) {
    if (response.status !== 403 || !preserveForbidden) { clearProtectedContext(); clearSession(); }
    throw { message: response.status === 401 ? "Sua sessão expirou. Entre novamente." : "Você não tem permissão para esta operação.", code: response.status === 401 ? "UNAUTHENTICATED" : "FORBIDDEN" } satisfies GraphQLFailure;
  }
  if (!response.ok || error) {
    if (["FORBIDDEN", "UNAUTHENTICATED", "TENANT_INACTIVE"].includes(error?.extensions?.code ?? "") && !(preserveForbidden && error?.extensions?.code === "FORBIDDEN")) { clearProtectedContext(); clearSession(); }
    const code = error?.extensions?.code;
    throw { message: transportFailureMessage(code), code, field: error?.extensions?.field, correlationId: error?.extensions?.correlationId } satisfies GraphQLFailure;
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

/** Queries an optional capability without turning a denied capability into a sign-out. */
export function graphqlOptionalPermission<T, V = unknown>(document: TypedDocumentNode<unknown, never>, variables?: V, signal?: AbortSignal): Promise<T> {
  return execute<T, V>(document, variables, signal, true, true);
}
