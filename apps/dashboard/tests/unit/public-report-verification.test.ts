import { describe, expect, it } from "vitest";
import { formatIssuedAt } from "@/features/dashboard/public-report-verification";

describe("public report verification dates", () => {
  it("uses the safe unavailable label for invalid dates", () => {
    expect(formatIssuedAt("data-invalida")).toBe("Data indisponível");
    expect(formatIssuedAt(null)).toBe("Data indisponível");
  });
});
