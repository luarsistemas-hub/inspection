import { clearOnboardingSession } from "@/auth/onboarding-session";
import { logoutOnboardingSession } from "@/graphql/client";

const defaultLogoutWaitMs = 2_000;
let pendingLogout: Promise<void> | undefined;

/** Clears local onboarding state and starts best-effort invalidation of the HttpOnly session cookie. */
export function restartOnboardingSession(endServerSession = true) {
  if (endServerSession) {
    const logout = logoutOnboardingSession({ clearLocal: false }).catch(() => undefined);
    pendingLogout = logout;
    void logout.finally(() => { if (pendingLogout === logout) pendingLogout = undefined; });
  }
  clearOnboardingSession();
}

/**
 * Waits briefly for the logout started by a restart. The server always answers
 * logout with an expiring session cookie, so a new identity request must not
 * race it or the newly issued cookie could be cleared afterwards.
 */
export async function waitForPendingOnboardingLogout(timeoutMs = defaultLogoutWaitMs): Promise<void> {
  const logout = pendingLogout;
  if (!logout) return;
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    await Promise.race([logout, new Promise<void>((resolve) => { timer = setTimeout(resolve, timeoutMs); })]);
  } finally {
    if (timer !== undefined) clearTimeout(timer);
  }
}
