import { describe, expect, it } from "vitest";
import { isCaptureSessionFailure } from "@/graphql/client";
import { CaptureMutationError, isCaptureConflict, throwOnUserErrors } from "@/pwa/mutation-errors";

describe("capture mutation errors", () => {
  it("turns a business rejection into an exception before local state changes", () => {
    expect(() => throwOnUserErrors({ userErrors: [{ message: "submission is not ready" }] })).toThrow("submission is not ready");
  });

  it("allows a successful payload to continue", () => {
    expect(() => throwOnUserErrors({ userErrors: [] })).not.toThrow();
  });

  it("preserves session error codes for the reauthentication classifier", () => {
    expect(() => throwOnUserErrors({ userErrors: [{ code: "SESSION_EXPIRED", message: "session expired" }] })).toThrow(CaptureMutationError);
    try {
      throwOnUserErrors({ userErrors: [{ code: "SESSION_EXPIRED", message: "session expired" }] });
    } catch (error) {
      expect(error).toMatchObject({ code: "SESSION_EXPIRED", message: "session expired" });
      expect(isCaptureSessionFailure(error)).toBe(true);
    }
  });

  it("classifies real capture conflict messages by code, not translated text", () => {
    for (const message of ["A resposta foi alterada. Atualize a captura…", "A declaração foi alterada…"]) {
      let failure: unknown;
      try { throwOnUserErrors({ userErrors: [{ code: "CONFLICT", message }] }); } catch (error) { failure = error; }
      expect(isCaptureConflict(failure)).toBe(true);
      expect(isCaptureConflict(new CaptureMutationError({ code: "INVALID_STATE", message }))).toBe(false);
    }
  });
});
