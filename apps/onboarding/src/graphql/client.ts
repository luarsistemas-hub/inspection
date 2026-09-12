import type { TypedDocumentNode } from "@graphql-typed-document-node/core";
import { print } from "graphql";
import { clearOnboardingSession, getOnboardingCsrfToken, setOnboardingCsrfToken } from "@/auth/onboarding-session";

export type GraphQLFailure = { message: string; code?: string; field?: string };
type Response<T> = { data?: T; errors?: Array<{ message: string; extensions?: { code?: string; field?: string } }> };

const endpoint = process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql";
const sessionErrors = new Set(["UNAUTHENTICATED", "SESSION_EXPIRED"]);

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
