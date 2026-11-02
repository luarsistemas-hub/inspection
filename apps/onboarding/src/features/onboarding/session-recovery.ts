type SessionFailure = { code?: string };
type UserError = { code?: string | null };

/** Returns true and invokes recovery when a GraphQL operation reports an expired session. */
export function recoverExpiredSessionFailure(error: unknown, recover: () => void) {
  if (typeof error !== "object" || error === null || !("code" in error) || !["UNAUTHENTICATED", "SESSION_EXPIRED"].includes(String((error as SessionFailure).code))) return false;
  recover();
  return true;
}

/** Returns true and invokes recovery when a mutation payload reports an expired session. */
export function recoverExpiredSessionUserErrors(errors: readonly UserError[], recover: () => void) {
  if (!errors.some((error) => error.code === "UNAUTHENTICATED" || error.code === "SESSION_EXPIRED")) return false;
  recover();
  return true;
}
