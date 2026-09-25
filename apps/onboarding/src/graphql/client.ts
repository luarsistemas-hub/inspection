import type { TypedDocumentNode } from "@graphql-typed-document-node/core";
import { getOperationAST, print } from "graphql";
import { clearOnboardingSession, getOnboardingCsrfToken, setOnboardingCsrfToken } from "@/auth/onboarding-session";

export type GraphQLFailure = { message: string; code?: string; field?: string };
type GraphQLErrorResponse = { message: string; extensions?: { code?: string; field?: string; correlationId?: string; [key: string]: unknown } };
type Response<T> = { data?: T; errors?: GraphQLErrorResponse[] };

const endpoint = process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql";
const sessionErrors = new Set(["UNAUTHENTICATED", "SESSION_EXPIRED"]);

function logOperationErrors<TData, TVariables>(query: TypedDocumentNode<TData, TVariables>, errors: GraphQLErrorResponse[]): void {
  const operation = getOperationAST(query);
  const operationLabel = operation ? `${operation.operation} ${operation.name?.value ?? "(anonymous)"}` : "unknown operation";
  console.error(`[GraphQL] ${operationLabel} failed`, { errors });
}

/** Sends a reference image through the private onboarding media endpoint. */
export async function uploadReferencePhoto(file: File, description: string, attentionItems: string[], id: string): Promise<string> {
  const form = new FormData();
  form.set("file", file);
  form.set("description", description);
	form.set("attentionItems", JSON.stringify(attentionItems));
  form.set("clientMutationId", id);
  const response = await fetch(new URL("/onboarding/reference-photos", endpoint), {
    method: "POST",
    credentials: "include",
    headers: { "X-CSRF-Token": getOnboardingCsrfToken() ?? "" },
    body: form,
  });
  const result = await response.json().catch(() => ({})) as { mediaId?: string; message?: string; code?: string };
  if (!response.ok || !result.mediaId) {
    const failure: GraphQLFailure = { message: result.message ?? "Não foi possível enviar a foto.", code: result.code };
    if (isOnboardingSessionFailure(failure)) clearOnboardingSession();
    throw failure;
  }
  return result.mediaId;
}

export const isOnboardingSessionFailure = (error: unknown): error is GraphQLFailure =>
  typeof error === "object" && error !== null && "code" in error && sessionErrors.has(String(error.code));

export function clientMutationId() {
  return globalThis.crypto?.randomUUID?.() ?? `onboarding-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

export function mapUserErrors(errors: ReadonlyArray<{ message: string; field?: string | null; code?: string | null }>) {
  return errors.reduce<Record<string, string>>((result, error) => {
    if (error.field) result[error.field] = error.message;
    return result;
  }, {});
}

export function graphql<TData>(query: TypedDocumentNode<TData, Record<string, never>>): Promise<TData>;
export function graphql<TData, TVariables>(query: TypedDocumentNode<TData, TVariables>, variables: TVariables): Promise<TData>;
export async function graphql<TData, TVariables>(query: TypedDocumentNode<TData, TVariables>, variables?: TVariables): Promise<TData> {
  const csrf = getOnboardingCsrfToken();
  const response = await fetch(endpoint, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json", ...(csrf ? { "X-CSRF-Token": csrf } : {}) },
    body: JSON.stringify({ query: print(query), variables }),
  });
  const nextCsrf = response.headers.get("x-csrf-token");
  if (nextCsrf) setOnboardingCsrfToken(nextCsrf);
  const raw = await response.text();
  let body: Response<TData> = {};
  if (raw.trimStart().startsWith("{")) {
    try { body = JSON.parse(raw) as Response<TData>; } catch { body = {}; }
  }
  const error = body.errors?.find((item) => sessionErrors.has(item.extensions?.code ?? "")) ?? body.errors?.[0];
  if (response.status === 401 || response.status === 403 || !response.ok || error) {
    if (body.errors?.length) logOperationErrors(query, body.errors);
    const failure: GraphQLFailure = {
      message: error?.message ?? (response.status === 401 ? "Sua sessão expirou. Recomece a verificação do e-mail." : "Não foi possível concluir a solicitação."),
      code: error?.extensions?.code ?? (response.status === 401 ? "UNAUTHENTICATED" : undefined),
      field: error?.extensions?.field,
    };
    if (isOnboardingSessionFailure(failure)) clearOnboardingSession();
    throw failure;
  }
  if (!body.data) throw { message: "Resposta vazia da API." } satisfies GraphQLFailure;
  return body.data;
}
