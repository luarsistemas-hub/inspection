import { describe, expect, it } from "vitest";
import { createTenantErrorMessage, localizeCreateTenantErrors } from "@/features/admin/create-tenant-errors";

describe("createTenant userErrors", () => {
  it("maps backend English messages to Portuguese by code and field", () => {
    expect(createTenantErrorMessage({ code: "INVALID_INPUT", field: "name", message: "name is required" })).toBe("Informe um nome válido para a operação, com até 200 caracteres.");
    expect(createTenantErrorMessage({ code: "CONFLICT", field: "identity", message: "identity is already provisioned" })).toBe("Sua conta já está vinculada a uma operação. Entre novamente para continuar.");
    expect(createTenantErrorMessage({ code: "INVALID_INPUT", field: "unknown", message: "bad" })).toBe("Revise os dados informados e tente novamente.");
    expect(createTenantErrorMessage({ code: "SOMETHING_NEW", field: null, message: "boom" })).toBe("Não foi possível concluir a solicitação. Tente novamente.");
  });

  it("never keeps the backend message", () => {
    const errors = localizeCreateTenantErrors([{ code: "CONFLICT", field: "idempotencyKey", message: "idempotency payload conflict", correlationId: "c-1" }]);
    expect(errors).toEqual([{ code: "CONFLICT", field: "idempotencyKey", message: "Esta solicitação já foi enviada com outros dados. Atualize a página e tente novamente.", correlationId: "c-1" }]);
  });
});
