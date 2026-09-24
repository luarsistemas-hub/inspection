import type { TypedDocumentNode } from "@graphql-typed-document-node/core";
import { getOperationAST, print } from "graphql";
import {
  AcceptProcessingDocument,
  CompleteMediaUploadDocument,
  CreateMediaUploadDocument,
  DeclareCaptureImpossibilityDocument,
  DeclareSensitiveDetectionFalsePositiveDocument,
  ExternalCaptureBootstrapDocument,
  PresignMediaPartsDocument,
  RequestInvitationOtpDocument,
  RevokeInvitationDocument,
  SaveCaptureMetadataDocument,
  SubmitCaptureDocument,
  SubmitRecaptureDocument,
  VerifyInvitationOtpDocument
} from "@/graphql/generated";
import { clearCaptureCsrfToken, getCaptureCsrfToken } from "@/auth/capture-session";

export type GraphQLFailure = { message: string; code?: string; field?: string };
type GraphQLErrorResponse = { message: string; extensions?: { code?: string; field?: string; correlationId?: string; [key: string]: unknown } };
type Response<T> = { data?: T; errors?: GraphQLErrorResponse[] };
const captureSessionErrorCodes = new Set(["UNAUTHENTICATED", "SESSION_EXPIRED"]);
const endpoint = process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql";

function logOperationErrors<TData, TVariables>(query: TypedDocumentNode<TData, TVariables>, errors: GraphQLErrorResponse[]): void {
  const operation = getOperationAST(query);
  const operationLabel = operation ? `${operation.operation} ${operation.name?.value ?? "(anonymous)"}` : "unknown operation";
  console.error(`[GraphQL] ${operationLabel} failed`, { errors });
}

export const isCaptureSessionFailure = (error: unknown): error is GraphQLFailure =>
  typeof error === "object" && error !== null && "code" in error && captureSessionErrorCodes.has(String(error.code));

export function graphql<TData>(query: TypedDocumentNode<TData, Record<string, never>>): Promise<TData>;
export function graphql<TData, TVariables>(query: TypedDocumentNode<TData, TVariables>, variables: TVariables): Promise<TData>;
export async function graphql<TData, TVariables>(query: TypedDocumentNode<TData, TVariables>, variables?: TVariables): Promise<TData> {
  const csrf = getCaptureCsrfToken();
  const response = await fetch(endpoint, {
    method: "POST", credentials: "include",
    headers: { "Content-Type": "application/json", ...(csrf ? { "X-CSRF-Token": csrf } : {}) },
    body: JSON.stringify({ query: print(query), variables })
  });
  let body: Response<TData> = {};
  const raw = await response.text();
  if (raw.trimStart().startsWith("{")) { try { body = JSON.parse(raw) as Response<TData>; } catch { body = {}; } }
  if (body.errors?.length) logOperationErrors(query, body.errors);
  if (response.status === 401 || response.status === 403) {
    if (response.status === 401) clearCaptureCsrfToken();
    throw { message: response.status === 401 ? "Sua sessão de captura expirou. Solicite um novo acesso." : "Você não tem permissão para esta operação.", code: response.status === 401 ? "UNAUTHENTICATED" : "FORBIDDEN" } satisfies GraphQLFailure;
  }
  const error = body.errors?.find((candidate) => captureSessionErrorCodes.has(candidate.extensions?.code ?? "")) ?? body.errors?.[0];
  if (!response.ok || error) {
    const failure = { message: error?.message ?? "Não foi possível concluir a solicitação", code: error?.extensions?.code, field: error?.extensions?.field } satisfies GraphQLFailure;
    if (isCaptureSessionFailure(failure)) clearCaptureCsrfToken();
    throw failure;
  }
  if (!body.data) throw { message: "Resposta vazia da API" } satisfies GraphQLFailure;
  return body.data;
}

export const captureOperations = {
  exchange: RequestInvitationOtpDocument,
  verifyOtp: VerifyInvitationOtpDocument,
  accept: AcceptProcessingDocument,
  revoke: RevokeInvitationDocument,
  bootstrap: ExternalCaptureBootstrapDocument,
  createUpload: CreateMediaUploadDocument,
  parts: PresignMediaPartsDocument,
  completeUpload: CompleteMediaUploadDocument,
  metadata: SaveCaptureMetadataDocument,
  impossibility: DeclareCaptureImpossibilityDocument,
  falsePositive: DeclareSensitiveDetectionFalsePositiveDocument,
  submit: SubmitCaptureDocument,
  submitRecapture: SubmitRecaptureDocument
};
