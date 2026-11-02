import { transportFailureMessage } from "@/graphql/client";

type CreateTenantUserError = { code: string; field?: string | null; message: string };

const invalidInputByField: Record<string, string> = {
  name: "Informe um nome válido para a operação, com até 200 caracteres.",
  timezone: "O fuso horário informado é inválido.",
  businessUnit: "Informe a unidade inicial da operação.",
  identity: "Não foi possível identificar sua conta. Entre novamente e tente outra vez.",
};

const conflictByField: Record<string, string> = {
  identity: "Sua conta já está vinculada a uma operação. Entre novamente para continuar.",
  idempotencyKey: "Esta solicitação já foi enviada com outros dados. Atualize a página e tente novamente.",
};

/** Maps createTenant userErrors, which the backend returns in English, to stable Portuguese messages. */
export function createTenantErrorMessage(error: CreateTenantUserError): string {
  const field = error.field ?? "";
  if (error.code === "INVALID_INPUT") return invalidInputByField[field] ?? transportFailureMessage(error.code);
  if (error.code === "CONFLICT") return conflictByField[field] ?? "Esta operação já foi criada. Atualize a página e tente novamente.";
  return transportFailureMessage(error.code);
}

/** Returns createTenant userErrors with Portuguese messages for the Admin UI. */
export function localizeCreateTenantErrors<T extends CreateTenantUserError>(errors: T[]): T[] {
  return errors.map((error) => ({ ...error, message: createTenantErrorMessage(error) }));
}
