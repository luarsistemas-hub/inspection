export type MutationUserError = { message: string; code?: string | null };

export class CaptureMutationError extends Error {
  readonly code: string | undefined;

  constructor(error: MutationUserError) {
    super(error.message);
    this.name = "CaptureMutationError";
    this.code = error.code ?? undefined;
  }
}

export function throwOnUserErrors(payload: { userErrors: MutationUserError[] }): void {
  const error = payload.userErrors[0];
  if (error) throw new CaptureMutationError(error);
}
