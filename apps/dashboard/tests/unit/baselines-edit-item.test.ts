import { describe, expect, it, vi } from "vitest";

vi.mock("@inspection/design-system", () => ({ Button: () => null, Field: () => null, Input: () => null, Select: () => null }));

import { editItemPatch } from "@/features/dashboard/baselines/review-forms";
import type { ReviewItem } from "@/features/dashboard/baselines/review-model";

const item = { id: "item-a", environmentId: "environment-a", observation: "OBSERVED", limitation: null, material: "Madeira", color: "Branco", quantity: 4 } as unknown as ReviewItem;
const values = { name: " Mesa ", typeCode: "TABLE", condition: "GOOD", material: "Madeira", color: "Branco", quantity: "4" };

describe("editItemPatch", () => {
  it("sends blank attributes as explicit unknown values instead of null", () => {
    const patch = editItemPatch(item, { ...values, material: "  ", color: "", quantity: "" });
    expect(patch.material).toBe("");
    expect(patch.color).toBe("");
    expect(patch.quantity).toBeNull();
    expect(patch.clearQuantity).toBe(true);
  });

  it("keeps filled attributes and does not clear the quantity", () => {
    const patch = editItemPatch(item, values);
    expect(patch).toMatchObject({ name: "Mesa", material: "Madeira", color: "Branco", quantity: 4, clearQuantity: null, environmentId: "environment-a" });
  });
});
