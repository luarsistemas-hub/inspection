const endpoint = process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql";

type Activation = { membershipId: string; email: string; name: string; newIdentity: boolean; status: string };
type UserError = { code: string; field: string | null; message: string };
type Result = { activation: Activation | null; userErrors: UserError[] };
type Body = { data?: Record<string, Result>; errors?: Array<{ message: string }> };
const storageKey = "inspection.user-invitation.csrf";

function mutationID(): string { return globalThis.crypto?.randomUUID?.() ?? `invite-${Date.now()}`; }

async function execute(field: string, input: Record<string, string | null>): Promise<Result> {
  const csrf = sessionStorage.getItem(storageKey);
  const response = await fetch(endpoint, {
    method: "POST", credentials: "include",
    headers: { "Content-Type": "application/json", ...(csrf ? { "X-CSRF-Token": csrf } : {}) },
    body: JSON.stringify({ query: `mutation InvitationActivation($input: ${field[0].toUpperCase()}${field.slice(1)}Input!) { ${field}(input: $input) { activation { membershipId email name newIdentity status } userErrors { code field message } } }`, variables: { input: { ...input, clientMutationId: mutationID() } } }),
  });
  const nextCsrf = response.headers.get("x-csrf-token");
  if (nextCsrf) sessionStorage.setItem(storageKey, nextCsrf);
  const body = await response.json().catch(() => ({})) as Body;
  const result = body.data?.[field];
  if (!response.ok || body.errors?.length || !result) throw new Error(body.errors?.[0]?.message ?? "Não foi possível concluir a ativação.");
  return result;
}

export async function requestInvitationCode(token?: string | null): Promise<Result> {
  const result = await execute("requestInternalUserActivationOtp", { invitationToken: token ?? null });
  if (token && typeof window !== "undefined") window.history.replaceState({}, "", window.location.pathname);
  return result;
}
export async function verifyInvitationCode(code: string): Promise<Result> {
  return execute("verifyInternalUserActivationOtp", { code });
}
export async function completeInvitation(password: string | null): Promise<Result> {
  const result = await execute("completeInternalUserActivation", { password });
  if (!result.userErrors.length) sessionStorage.removeItem(storageKey);
  return result;
}
export function clearInvitationProof(): void { sessionStorage.removeItem(storageKey); }
