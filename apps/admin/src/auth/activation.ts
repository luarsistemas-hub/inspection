const endpoint = process.env.NEXT_PUBLIC_INSPECTION_API_URL ?? "http://localhost:8080/graphql";

type UserError = { code: string; field: string | null; message: string };
type Payload = { userErrors: UserError[] };
type GraphQLErrorResponse = { message: string; extensions?: Record<string, unknown> };
type Response = { data?: Record<string, Payload>; errors?: GraphQLErrorResponse[] };

let csrfToken: string | undefined;

function logOperationErrors(operation: string, errors: GraphQLErrorResponse[]): void {
  console.error(`[GraphQL] ${operation} failed`, { errors });
}

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
  if (!response.ok || body.errors?.[0] || !payload) {
    if (body.errors?.length) logOperationErrors(`mutation ${operation}`, body.errors);
    throw new Error(body.errors?.[0]?.message ?? "Não foi possível concluir a ativação.");
  }
  return payload;
}

async function refreshActivationProof(): Promise<void> {
  if (csrfToken) return;
  const response = await fetch(endpoint, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ query: "query AdminActivationSession { onboardingSession { id } }" }),
  });
  const nextCsrf = response.headers.get("x-csrf-token");
  const body = await response.json().catch(() => ({})) as { data?: { onboardingSession?: { id: string } | null }; errors?: GraphQLErrorResponse[] };
  if (!response.ok || body.errors?.[0] || !body.data?.onboardingSession || !nextCsrf) {
    if (body.errors?.length) logOperationErrors("query AdminActivationSession", body.errors);
    throw new Error(body.errors?.[0]?.message ?? "Abra o link de ativação enviado para o seu e-mail. O link pode estar ausente, inválido ou expirado.");
  }
  csrfToken = nextCsrf;
}

export async function requestActivationCode(token?: string | null): Promise<UserError[]> {
  if (token) {
    const payload = await execute("RequestAdminActivationOtp", { activationToken: token });
    if (csrfToken && typeof window !== "undefined") {
      window.history.replaceState({}, "", window.location.pathname);
    }
    return payload.userErrors.map((error) => error.code === "SESSION_EXPIRED" ? { ...error, message: "Este link de ativação é inválido, já foi utilizado ou expirou." } : error);
  }
  await refreshActivationProof();
  return (await execute("RequestAdminActivationOtp", {})).userErrors;
}

export async function verifyActivationCode(code: string): Promise<UserError[]> {
  return (await execute("VerifyAdminActivationOtp", { code })).userErrors;
}

export async function setInitialPassword(password: string): Promise<UserError[]> {
  return (await execute("SetAdminInitialPassword", { password })).userErrors;
}

export function clearActivationProof(): void { csrfToken = undefined; }
