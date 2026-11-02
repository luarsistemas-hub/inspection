import { describe, expect, it } from "vitest";
import { matchesEmailConfirmation } from "@/features/admin/email-confirmation";

describe("Admin email correction", () => {
  it("IT-076 compares normalized values before allowing the resend", () => {
    expect(matchesEmailConfirmation(" Ana@Example.test ", "ana@example.test")).toBe(true);
    expect(matchesEmailConfirmation("ana@example.test", "other@example.test")).toBe(false);
  });
});
