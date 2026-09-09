import { describe, expect, it } from "vitest";
import { isCaptureSessionFailure } from "@/graphql/client";
import { CaptureMutationError, throwOnUserErrors } from "@/pwa/mutation-errors";

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
});
