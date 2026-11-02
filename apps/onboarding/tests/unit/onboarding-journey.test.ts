import { describe, expect, it, vi } from "vitest";
import { setOnboardingCsrfToken, getOnboardingCsrfToken } from "@/auth/onboarding-session";
import { restartOnboardingSession, waitForPendingOnboardingLogout } from "@/features/onboarding/restart-session";
import { recoverExpiredSessionFailure, recoverExpiredSessionUserErrors } from "@/features/onboarding/session-recovery";
import { routeOnboardingUserErrors } from "@/features/onboarding/user-errors";

const { logoutOnboardingSession } = vi.hoisted(() => ({ logoutOnboardingSession: vi.fn() }));
vi.mock("@/graphql/client", async (importOriginal) => ({
  ...await importOriginal<typeof import("@/graphql/client")>(),
  logoutOnboardingSession,
}));

describe("onboarding journey errors and restart", () => {
  it("routes an expectedVersion conflict to the general alert", () => {
    const result = routeOnboardingUserErrors([{ field: "expectedVersion", message: "Os dados foram atualizados. Confira a etapa novamente." }], ["name"]);
    expect(result.fieldErrors).toEqual({});
    expect(result.generalError).toBe("Os dados foram atualizados. Confira a etapa novamente.");
  });

  it("routes an already-confirmed step message to the general alert", () => {
    const result = routeOnboardingUserErrors([{ field: "step", message: "Esta etapa já foi confirmada." }], ["name"]);
    expect(result.fieldErrors).toEqual({});
    expect(result.generalError).toBe("Esta etapa já foi confirmada.");
  });

  it("ends the HttpOnly session without waiting for logout success", () => {
    setOnboardingCsrfToken("csrf-proof");
    logoutOnboardingSession.mockRejectedValueOnce(new Error("offline"));
    expect(() => restartOnboardingSession()).not.toThrow();
    expect(logoutOnboardingSession).toHaveBeenCalledOnce();
    expect(logoutOnboardingSession).toHaveBeenCalledWith({ clearLocal: false });
    expect(getOnboardingCsrfToken()).toBeUndefined();
  });

  it("holds a new identity request until the restart logout settles", async () => {
    let finishLogout!: () => void;
    logoutOnboardingSession.mockReturnValueOnce(new Promise<void>((resolve) => { finishLogout = resolve; }));
    restartOnboardingSession();
    let released = false;
    const waiting = waitForPendingOnboardingLogout(60_000).then(() => { released = true; });
    await Promise.resolve();
    await Promise.resolve();
    expect(released).toBe(false);
    finishLogout();
    await waiting;
    expect(released).toBe(true);
    await expect(waitForPendingOnboardingLogout(60_000)).resolves.toBeUndefined();
  });

  it("stops waiting for a stalled restart logout after the short timeout", async () => {
    vi.useFakeTimers();
    try {
      logoutOnboardingSession.mockReturnValueOnce(new Promise<void>(() => undefined));
      restartOnboardingSession();
      let released = false;
      const waiting = waitForPendingOnboardingLogout(1_500).then(() => { released = true; });
      await vi.advanceTimersByTimeAsync(1_499);
      expect(released).toBe(false);
      await vi.advanceTimersByTimeAsync(1);
      await waiting;
      expect(released).toBe(true);
    } finally {
      vi.useRealTimers();
    }
  });

  it("does not delay the identity request once the restart logout settled", async () => {
    logoutOnboardingSession.mockResolvedValueOnce(undefined);
    restartOnboardingSession();
    await waitForPendingOnboardingLogout(60_000);
    logoutOnboardingSession.mockClear();
    restartOnboardingSession(false);
    expect(logoutOnboardingSession).not.toHaveBeenCalled();
    let released = false;
    const waiting = waitForPendingOnboardingLogout(60_000).then(() => { released = true; });
    await Promise.resolve();
    await Promise.resolve();
    expect(released).toBe(true);
    await waiting;
  });

  it("returns review to the entry when completion reports SESSION_EXPIRED", () => {
    const recover = vi.fn();
    expect(recoverExpiredSessionUserErrors([{ code: "SESSION_EXPIRED" }], recover)).toBe(true);
    expect(recover).toHaveBeenCalledOnce();
  });

  it("returns /complete to the entry when its status query session expires", () => {
    const recover = vi.fn();
    expect(recoverExpiredSessionFailure({ code: "SESSION_EXPIRED" }, recover)).toBe(true);
    expect(recover).toHaveBeenCalledOnce();
  });
});
