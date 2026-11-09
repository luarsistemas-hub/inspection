import { describe, expect, it } from "vitest";
import { buildTemplateScopeV3, normalizeTemplateScope, validateTemplateScope, type ExpectedRoomDraft } from "../../src/features/admin/template-v2";

const kitchen: ExpectedRoomDraft[] = [{ key: "kitchen", typeCode: "KITCHEN", name: "Cozinha", components: [
  { key: "piso", typeCode: "FLOOR", label: "Piso", views: ["geral"] },
  { key: "armarios", typeCode: "CABINET", label: "Armários", views: [] },
] }];

describe("template scope v3", () => {
  it("UT-092: inclui componentes esperados ao publicar escopo", () => {
    expect(validateTemplateScope(kitchen)).toBeUndefined();
    expect(buildTemplateScopeV3("PROPERTY_COMPONENTS", kitchen, []).expectedRooms[0].components.map((item) => item.label)).toEqual(["Piso", "Armários"]);
  });
  it("UT-093: informa chave duplicada e preserva os dados do rascunho", () => {
    const draft = [{ ...kitchen[0], components: [...kitchen[0].components, { key: "piso", typeCode: "FLOOR", label: "Piso adicional", views: [] }] }];
    expect(validateTemplateScope(draft)).toContain("duplicada");
    expect(draft[0].components[2].label).toBe("Piso adicional");
  });
  it("faz round-trip de vistas string do template v3 e tolera listas nulas", () => {
    const payload = buildTemplateScopeV3("PROPERTY_COMPONENTS", kitchen, []);
    const serialized = JSON.parse(JSON.stringify(payload)) as { expectedRooms: ExpectedRoomDraft[] };
    expect(serialized.expectedRooms[0].components[0].views).toEqual(["geral"]);
    expect(normalizeTemplateScope([{ ...serialized.expectedRooms[0], components: null as never }])[0].components).toEqual([]);
    expect(validateTemplateScope(serialized.expectedRooms)).toBeUndefined();
  });
  it("valida chave de ambiente duplicada e chave de vista em formato slug", () => {
    expect(validateTemplateScope([kitchen[0], kitchen[0]])).toContain("ambiente");
    const invalid = [{ ...kitchen[0], components: [{ ...kitchen[0].components[0], views: ["Vista Geral"] }] }];
    expect(validateTemplateScope(invalid)).toContain("vista");
  });
});
