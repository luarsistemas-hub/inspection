import { beforeEach, describe, expect, it } from "vitest";
import { clearCaptureCsrfToken, getCaptureCsrfToken, setCaptureCsrfToken } from "@/auth/capture-session";

describe("external capture CSRF proof", () => {
  beforeEach(() => clearCaptureCsrfToken());

  it("keeps the proof in memory and clears it explicitly", () => {
    setCaptureCsrfToken("csrf-test");
    expect(getCaptureCsrfToken()).toBe("csrf-test");
    clearCaptureCsrfToken();
    expect(getCaptureCsrfToken()).toBeUndefined();
  });
});
