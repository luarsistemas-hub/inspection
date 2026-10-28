import * as React from "react";
import { act, createElement, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { AssetAttributeFields, type SegmentAttributeSchema } from "@/features/admin/asset-attributes";

vi.mock("@inspection/design-system", async () => {
  const React = await import("react");
  return {
    Field: ({ label, children }: { label: string; children: ReactNode }) => React.createElement("label", null, label, children),
    Input: (props: InputHTMLAttributes<HTMLInputElement>) => React.createElement("input", props),
    Select: (props: SelectHTMLAttributes<HTMLSelectElement>) => React.createElement("select", props),
  };
});

async function render(schema: SegmentAttributeSchema, values: Record<string, unknown>) {
  const host = document.createElement("div"); const root = createRoot(host);
  await act(async () => { root.render(createElement(AssetAttributeFields, { schema, values, onChange: () => undefined })); });
  return { host, unmount: () => act(async () => { root.unmount(); }) };
}
const optionTexts = (select: HTMLSelectElement | null) => [...(select?.querySelectorAll("option") ?? [])].map((option) => option.textContent);

describe("AssetAttributeFields", () => {
  Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, React });
  const freeStrings: SegmentAttributeSchema = { required: ["propertyType", "purpose"], properties: { propertyType: { type: "string", maxLength: 100 }, purpose: { type: "string", maxLength: 100 } } };

  it("renders onboarding free-string attributes as Portuguese combos", async () => {
    const { host, unmount } = await render(freeStrings, { propertyType: "APARTMENT", purpose: "SALE" });
    const [type, purpose] = [...host.querySelectorAll("select")];
    expect(host.querySelectorAll("input")).toHaveLength(0);
    expect(optionTexts(type)).toEqual(["Selecione", "Apartamento", "Casa", "Imóvel comercial", "Terreno"]);
    expect(optionTexts(purpose)).toEqual(["Selecione", "Venda", "Locação", "Manutenção", "Seguro"]);
    expect([type.value, purpose.value]).toEqual(["APARTMENT", "SALE"]);
    await unmount();
  });

  it("keeps a stored value outside the vocabulary selectable", async () => {
    const { host, unmount } = await render(freeStrings, { propertyType: "residential" });
    const type = host.querySelector("select");
    expect(optionTexts(type)).toContain("residential");
    expect(type?.value).toBe("residential");
    await unmount();
  });

  it("prefers the schema enum and leaves other free strings as text", async () => {
    const { host, unmount } = await render({ properties: { propertyType: { type: "string", enum: ["HOUSE", "LAND"] }, identifier: { type: "string" } } }, {});
    expect(optionTexts(host.querySelector("select"))).toEqual(["Selecione", "Casa", "Terreno"]);
    expect(host.querySelectorAll("input")).toHaveLength(1);
    await unmount();
  });
});
