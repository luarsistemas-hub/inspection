import { clearCaptureCsrfToken, getCaptureCsrfToken } from "@/auth/capture-session";

export type GraphQLFailure = { message: string; code?: string; field?: string };
type Response<T> = { data?: T; errors?: Array<{ message: string; extensions?: { code?: string; field?: string } }> };
const endpoint = process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql";

export async function graphql<T>(query: string, variables?: Record<string, unknown>): Promise<T> {
  const csrf = getCaptureCsrfToken();
  const response = await fetch(endpoint, {
    method: "POST", credentials: "include",
    headers: { "Content-Type": "application/json", ...(csrf ? { "X-CSRF-Token": csrf } : {}) },
    body: JSON.stringify({ query, variables })
  });
  let body: Response<T> = {};
  const raw = await response.text();
  if (raw.trimStart().startsWith("{")) { try { body = JSON.parse(raw) as Response<T>; } catch { body = {}; } }
  if (response.status === 401 || response.status === 403) {
    clearCaptureCsrfToken();
    throw { message: response.status === 401 ? "Sua sessão de captura expirou. Solicite um novo acesso." : "Você não tem permissão para esta operação.", code: response.status === 401 ? "UNAUTHENTICATED" : "FORBIDDEN" } satisfies GraphQLFailure;
  }
  const error = body.errors?.[0];
  if (!response.ok || error) throw { message: error?.message ?? "Não foi possível concluir a solicitação", code: error?.extensions?.code, field: error?.extensions?.field } satisfies GraphQLFailure;
  if (!body.data) throw { message: "Resposta vazia da API" } satisfies GraphQLFailure;
  return body.data;
}

export const captureOperations = {
  exchange: "mutation Exchange($linkToken:String!,$id:String!){requestInvitationOtp(input:{linkToken:$linkToken,clientMutationId:$id}){status userErrors{message code}}}",
  verifyOtp: "mutation Verify($linkToken:String!,$code:String!,$id:String!){verifyInvitationOtp(input:{linkToken:$linkToken,code:$code,clientMutationId:$id}){status csrfToken userErrors{message code}}}",
  accept: "mutation Accept($disclosureVersion:String!,$photoProcessing:Boolean!,$aiAnalysis:Boolean!,$gpsUse:Boolean!,$id:String!){acceptProcessing(input:{disclosureVersion:$disclosureVersion,photoProcessing:$photoProcessing,aiAnalysis:$aiAnalysis,gpsUse:$gpsUse,clientMutationId:$id}){status userErrors{message code}}}",
  revoke: "mutation Revoke($linkToken:String!,$id:String!){revokeInvitation(input:{linkToken:$linkToken,clientMutationId:$id}){status userErrors{message code}}}",
  bootstrap: "query Capture{externalCapture{responsibilityId recaptureRequestId status confirmationOnly kind disclosureVersion reference policy requirements{key section label instructions required minimumMedia maximumMedia descriptionRequired impossibilityAllowed} answers{requirementKey mediaIds impossibilityReason version}}}",
  createUpload: "mutation Upload($contentType:String!,$size:Int!,$hash:String!,$id:String!){createMediaUpload(input:{contentType:$contentType,sizeBytes:$size,sha256:$hash,clientMutationId:$id}){upload{mediaId uploadId}userErrors{message code}}}",
  parts: "mutation Parts($mediaId:ID!,$parts:[Int!]!,$id:String!){presignMediaParts(input:{mediaId:$mediaId,partNumbers:$parts,clientMutationId:$id}){parts{partNumber url}userErrors{message code}}}",
  completeUpload: "mutation Complete($mediaId:ID!,$parts:[CompletedPartInput!]!,$id:String!){completeMediaUpload(input:{mediaId:$mediaId,parts:$parts,clientMutationId:$id}){media{id}userErrors{message code}}}",
  metadata: "mutation Metadata($mediaId:ID!,$key:String!,$description:String!,$id:String!){saveCaptureMetadata(input:{mediaId:$mediaId,requirementKey:$key,description:$description,captureSource:CAMERA,clientMutationId:$id}){media{id}userErrors{message code}}}",
  impossibility: "mutation Impossible($key:String!,$reason:String!,$id:String!){declareCaptureImpossibility(input:{requirementKey:$key,reason:$reason,clientMutationId:$id}){status userErrors{message code}}}",
  submit: "mutation Submit($incomplete:Boolean!,$id:String!){submitCapture(input:{confirmIncomplete:$incomplete,clientMutationId:$id}){submission{id}userErrors{message code}}}",
  submitRecapture: "mutation SubmitRecapture($requestId:ID!,$incomplete:Boolean!,$id:String!){submitRecapture(input:{requestId:$requestId,confirmIncomplete:$incomplete,clientMutationId:$id}){recapture{id}userErrors{message code}}}"
};
