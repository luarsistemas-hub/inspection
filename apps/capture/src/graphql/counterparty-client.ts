import type { TypedDocumentNode } from "@graphql-typed-document-node/core";
import { print } from "graphql";
import { getCounterpartyCsrfToken, clearCounterpartyCsrfToken } from "@/auth/counterparty-session";
import { AcknowledgeBaselineDocument, ContestBaselineDocument, CounterpartyBaselineDocument, RequestCounterpartyOtpDocument, VerifyCounterpartyOtpDocument } from "@/graphql/generated";

const endpoint = process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql";
export const counterpartyOperations = {
  requestOtp: RequestCounterpartyOtpDocument,
  verifyOtp: VerifyCounterpartyOtpDocument,
  baseline: CounterpartyBaselineDocument,
  acknowledge: AcknowledgeBaselineDocument,
  contest: ContestBaselineDocument
};

type ErrorBody = { message?: string; extensions?: { code?: string } };
export async function counterpartyGraphql<TData, TVariables>(document: TypedDocumentNode<TData, TVariables>, variables?: TVariables): Promise<TData> {
  const csrfToken = getCounterpartyCsrfToken();
  const response = await fetch(endpoint, { method: "POST", credentials: "include", headers: { "Content-Type": "application/json", ...(csrfToken ? { "X-CSRF-Token": csrfToken } : {}) }, body: JSON.stringify({ query: print(document), variables }) });
  const body = await response.json() as { data?: TData; errors?: ErrorBody[] };
  if (response.status === 401) clearCounterpartyCsrfToken();
  if (!response.ok || body.errors?.length) throw new Error(body.errors?.[0]?.message ?? "Não foi possível concluir a solicitação.");
  if (!body.data) throw new Error("Resposta vazia da API.");
  return body.data;
}
