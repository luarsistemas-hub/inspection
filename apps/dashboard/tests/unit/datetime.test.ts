import { describe, expect, it } from "vitest";
import { localDateTimeToInstant } from "@/features/dashboard/datetime";

describe("inspection form dates", () => {
  it("sends a datetime-local value as an instant with an explicit offset", () => {
    const local = "2026-09-25T23:32";
    const result = localDateTimeToInstant(local);

    expect(result).toMatch(/^2026-09-\d{2}T\d{2}:\d{2}:00\.000Z$/);
    expect(Date.parse(result)).toBe(new Date(local).getTime());
  });

  it("rejects a malformed local date before sending the mutation", () => {
    expect(() => localDateTimeToInstant("invalid")).toThrow(RangeError);
  });
});
