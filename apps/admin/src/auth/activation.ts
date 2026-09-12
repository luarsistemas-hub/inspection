const endpoint = process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql";

type UserError = { code: string; field: string | null; message: string };
type Payload = { userErrors: UserError[] };
type Response = { data?: Record<string, Payload>; errors?: Array<{ message: string }> };

let csrfToken: string | undefined;

function mutationID(): string {
  return globalThis.crypto?.randomUUID?.() ?? `activation-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

async function execute(operation: string, input: Record<string, string>): Promise<Payload> {
  const response = await fetch(endpoint, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json", ...(csrfToken ? { "X-CSRF-Token": csrfToken } : {}) },
    body: JSON.stringify({ query: `mutation Activation($input: ${operation}Input!) { ${operation.charAt(0).toLowerCase()}${operation.slice(1)}(input: $input) { userErrors { code field message } } }`, variables: { input: { ...input, clientMutationId: mutationID() } } }),
  });
  const nextCsrf = response.headers.get("x-csrf-token");
  if (nextCsrf) csrfToken = nextCsrf;
  const body = await response.json().catch(() => ({})) as Response;
  const payload = body.data?.[`${operation.charAt(0).toLowerCase()}${operation.slice(1)}`];
  if (!response.ok || body.errors?.[0] || !payload) throw new Error(body.errors?.[0]?.message ?? "Não foi possível concluir a ativação.");
  return payload;
}

export async function requestActivationCode(): Promise<UserError[]> {
  return (await execute("RequestAdminActivationOtp", {})).userErrors;
}

export async function verifyActivationCode(code: string): Promise<UserError[]> {
  return (await execute("VerifyAdminActivationOtp", { code })).userErrors;
}

export async function setInitialPassword(password: string): Promise<UserError[]> {
  return (await execute("SetAdminInitialPassword", { password })).userErrors;
}

export function clearActivationProof(): void { csrfToken = undefined; }
